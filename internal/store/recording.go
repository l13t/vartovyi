package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
	"vartovyi/internal/model"
)

var ErrGroupInUse = errors.New("camera group is in use")

func (s *Store) Recording(ctx context.Context) (model.RecordingSettings, error) {
	v := model.RecordingSettings{Groups: []model.CameraGroup{}}
	var b []byte
	e := s.Pool.QueryRow(ctx, "SELECT config,revision FROM recording_settings WHERE id=1").Scan(&b, &v.Revision)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	revision := v.Revision
	e = json.Unmarshal(b, &v)
	v.Revision = revision
	return v, e
}
func (s *Store) SaveRecording(ctx context.Context, v model.RecordingSettings) error {
	if v.Groups == nil {
		v.Groups = []model.CameraGroup{}
	}
	if e := v.Validate(); e != nil {
		return e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(712039444)"); e != nil {
		return e
	}
	var revision int64
	e = tx.QueryRow(ctx, "SELECT revision FROM recording_settings WHERE id=1").Scan(&revision)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if revision != v.Revision {
		return ErrConflict
	}
	rows, e := tx.Query(ctx, "SELECT DISTINCT coalesce(config->>'group_id','') FROM api_cameras")
	if e != nil {
		return e
	}
	for rows.Next() {
		var group string
		if e = rows.Scan(&group); e != nil {
			rows.Close()
			return e
		}
		if !v.HasGroup(group) {
			rows.Close()
			return ErrGroupInUse
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	v.Revision++
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, "INSERT INTO recording_settings(id,config,revision) VALUES(1,$1,$2) ON CONFLICT(id) DO UPDATE SET config=$1,revision=$2", b, v.Revision)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// RetentionCandidates uses the shared catalog across all shards. Window stages
// remove camera excess first, then group excess, then global excess. Protected
// recent files count toward usage but are never deletion candidates. Results are
// bounded; only the owning recorder may remove the actual files.
func (s *Store) RetentionCandidates(ctx context.Context, settings model.RecordingSettings, cameras []model.Camera, shard string) (map[string]bool, error) {
	type limits struct {
		ID      string `json:"id"`
		GroupID string `json:"group_id"`
		Days    int    `json:"days"`
		Count   int64  `json:"count"`
		Bytes   int64  `json:"bytes"`
		Buffer  int    `json:"buffer"`
	}
	cs := []limits{}
	for _, c := range cameras {
		cs = append(cs, limits{c.ID, c.GroupID, settings.AgeDays(c), c.MaxSegments, c.QuotaBytes, 80 + c.PreSeconds + c.PostSeconds})
	}
	gs := []limits{}
	for _, g := range settings.Groups {
		gs = append(gs, limits{ID: g.ID, Count: g.Limits.MaxSegments, Bytes: g.Limits.MaxBytes})
	}
	cb, e := json.Marshal(cs)
	if e != nil {
		return nil, e
	}
	gb, e := json.Marshal(gs)
	if e != nil {
		return nil, e
	}
	rows, e := s.Pool.Query(ctx, `WITH cameras AS (SELECT * FROM jsonb_to_recordset($1::jsonb) AS c(id text,group_id text,days int,count bigint,bytes bigint,buffer int)),
 groups AS (SELECT * FROM jsonb_to_recordset($2::jsonb) AS g(id text,count bigint,bytes bigint)),
 base AS (SELECT s.*,c.group_id,c.count AS camera_count,c.bytes AS camera_bytes,g.count AS group_count,g.bytes AS group_bytes,
 s.end_at < now()-make_interval(secs=>c.buffer) AS eligible,
 c.days>0 AND s.end_at<now()-make_interval(days=>c.days) AS expired
 FROM recorder_segments s JOIN cameras c ON c.id=s.camera_id LEFT JOIN groups g ON g.id=c.group_id),
 expired AS (SELECT * FROM base WHERE eligible AND (expired OR NOT keep)),
 cw AS (SELECT *,row_number() OVER w AS n,sum(bytes) OVER w AS used FROM base WHERE id NOT IN (SELECT id FROM expired) WINDOW w AS(PARTITION BY camera_id ORDER BY eligible ASC,end_at DESC,id DESC ROWS UNBOUNDED PRECEDING)),
 cr AS (SELECT * FROM cw WHERE eligible AND ((camera_count>0 AND n>camera_count) OR (camera_bytes>0 AND used>camera_bytes))),
 gw AS (SELECT *,row_number() OVER w AS gn,sum(bytes) OVER w AS gu FROM cw WHERE id NOT IN (SELECT id FROM cr) WINDOW w AS(PARTITION BY group_id ORDER BY eligible ASC,end_at DESC,id DESC ROWS UNBOUNDED PRECEDING)),
 gr AS (SELECT * FROM gw WHERE eligible AND ((group_count>0 AND gn>group_count) OR (group_bytes>0 AND gu>group_bytes))),
 aw AS (SELECT *,row_number() OVER w AS an,sum(bytes) OVER w AS au FROM gw WHERE id NOT IN(SELECT id FROM gr) WINDOW w AS(ORDER BY eligible ASC,end_at DESC,id DESC ROWS UNBOUNDED PRECEDING)),
 ar AS (SELECT * FROM aw WHERE eligible AND (($3::bigint>0 AND an>$3) OR ($4::bigint>0 AND au>$4))),
 selected AS(SELECT id,shard_id,end_at FROM expired UNION SELECT id,shard_id,end_at FROM cr UNION SELECT id,shard_id,end_at FROM gr UNION SELECT id,shard_id,end_at FROM ar)
 SELECT id FROM selected WHERE shard_id=$5 ORDER BY end_at,id LIMIT 1000`, cb, gb, settings.Global.MaxSegments, settings.Global.MaxBytes, shard)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// LockRetention serializes a pruning pass with group/policy/camera changes and
// other recorders, so a committed policy expansion cannot race an old plan.
func (s *Store) LockRetention(ctx context.Context) (func(), error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	release := func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(712039444)"); e != nil {
		release()
		return nil, e
	}
	return release, nil
}
