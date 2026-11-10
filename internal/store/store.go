package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/trace"
	"vartovyi/internal/auth"
	"vartovyi/internal/model"
	"vartovyi/internal/telemetry"
)

//go:embed schema.sql
var schema string
var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrCurrentPassword = errors.New("incorrect current password")
var ErrExternalPassword = errors.New("password is managed externally")
var ErrInvalidLanguage = errors.New("unsupported language")
var ErrInvalidProfile = errors.New("invalid profile")
var ErrWeakPassword = errors.New("password must be 12 to 1024 bytes")

type Store struct {
	Pool   *pgxpool.Pool
	cipher *auth.Cipher
}

func Open(ctx context.Context, url, key string) (*Store, error) {
	if url == "" {
		return nil, errors.New("database_url is required")
	}
	c, e := auth.NewCipher(key)
	if e != nil {
		return nil, e
	}
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil {
		return nil, errors.New("invalid database configuration")
	}
	cfg.ConnConfig.Tracer = telemetry.QueryTracer{}
	cfg.MaxConns = 8
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return nil, e
	}
	return &Store{p, c}, nil
}
func (s *Store) Close() { s.Pool.Close() }
func (s *Store) Ping(ctx context.Context) error {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.Pool.Ping(c)
}
func (s *Store) Migrate(ctx context.Context) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(712039441)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, schema); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) Bootstrap(ctx context.Context, name, password string) error {
	var n int
	if e := s.Pool.QueryRow(ctx, "SELECT count(*) FROM api_users").Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	var h string
	var e error
	if name == "admin" && password == "admin" {
		h, e = auth.HashDefaultAdminPassword()
	} else {
		h, e = auth.HashPassword(password)
	}
	if e != nil {
		return e
	}
	_, e = s.Pool.Exec(ctx, "INSERT INTO api_users(id,username,role,password_hash) VALUES($1,$2,'admin',$3) ON CONFLICT DO NOTHING", model.ID(), name, h)
	return e
}
func scanUser(row pgx.Row) (model.User, error) {
	var u model.User
	e := row.Scan(&u.ID, &u.Username, &u.Role, &u.PasswordHash, &u.Subject, &u.Name, &u.Language)
	return u, e
}

const userCols = "id,username,role,password_hash,coalesce(subject,''),display_name,language"

func (s *Store) User(ctx context.Context, id string) (model.User, error) {
	return scanUser(s.Pool.QueryRow(ctx, "SELECT "+userCols+" FROM api_users WHERE id=$1", id))
}

func (s *Store) UpdateProfile(ctx context.Context, id string, v model.ProfileChanges) (model.User, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return model.User{}, e
	}
	defer tx.Rollback(ctx)
	u, e := scanUser(tx.QueryRow(ctx, "SELECT "+userCols+" FROM api_users WHERE id=$1 FOR UPDATE", id))
	if e != nil {
		return u, e
	}
	if v.Language != nil {
		if *v.Language != "uk" && *v.Language != "en" {
			return u, ErrInvalidLanguage
		}
		u.Language = *v.Language
	}
	if v.Name != nil {
		name := strings.TrimSpace(*v.Name)
		if name == "" || utf8.RuneCountInString(name) > 100 {
			return u, ErrInvalidProfile
		}
		u.Name = name
	}
	if v.NewPassword != "" {
		if u.PasswordHash == "" {
			return u, ErrExternalPassword
		}
		if !auth.VerifyPassword(u.PasswordHash, v.CurrentPassword) {
			return u, ErrCurrentPassword
		}
		if len(v.NewPassword) < 12 || len(v.NewPassword) > 1024 {
			return u, ErrWeakPassword
		}
		u.PasswordHash, e = auth.HashPassword(v.NewPassword)
		if e != nil {
			return u, e
		}
	}
	if _, e = tx.Exec(ctx, "UPDATE api_users SET display_name=$2,password_hash=$3,language=$4 WHERE id=$1", id, u.Name, u.PasswordHash, u.Language); e != nil {
		return u, e
	}
	if v.NewPassword != "" {
		if _, e = tx.Exec(ctx, "DELETE FROM api_sessions WHERE user_id=$1", id); e != nil {
			return u, e
		}
	}
	return u, tx.Commit(ctx)
}

func (s *Store) UserByName(ctx context.Context, name string) (model.User, error) {
	return scanUser(s.Pool.QueryRow(ctx, "SELECT "+userCols+" FROM api_users WHERE username=$1", name))
}
func (s *Store) Users(ctx context.Context) ([]model.User, error) {
	rows, e := s.Pool.Query(ctx, "SELECT "+userCols+" FROM api_users ORDER BY username LIMIT 1000")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.User{}
	for rows.Next() {
		u, e := scanUser(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Store) SaveUser(ctx context.Context, u model.User, password string) error {
	if !u.Role.Valid() || len(u.Username) < 1 || len(u.Username) > 200 || utf8.RuneCountInString(u.Name) > 100 {
		return errors.New("invalid user")
	}
	if u.ID == "" {
		u.ID = model.ID()
	}
	h := ""
	var e error
	if password != "" {
		h, e = auth.HashPassword(password)
		if e != nil {
			return e
		}
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(712039442)"); e != nil {
		return e
	}
	var current model.Role
	_ = tx.QueryRow(ctx, "SELECT role FROM api_users WHERE id=$1", u.ID).Scan(&current)
	if current == model.Admin && u.Role != model.Admin {
		var n int
		if e = tx.QueryRow(ctx, "SELECT count(*) FROM api_users WHERE role='admin'").Scan(&n); e != nil {
			return e
		}
		if n < 2 {
			return errors.New("cannot demote last administrator")
		}
	}
	if current == "" && h == "" {
		return errors.New("new local user requires password")
	}
	_, e = tx.Exec(ctx, "INSERT INTO api_users(id,username,role,password_hash,display_name) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET username=$2,role=$3,display_name=$5,password_hash=CASE WHEN $4='' THEN api_users.password_hash ELSE $4 END", u.ID, u.Username, u.Role, h, u.Name)
	if e != nil {
		return e
	}
	if h != "" || current != u.Role {
		_, e = tx.Exec(ctx, "DELETE FROM api_sessions WHERE user_id=$1", u.ID)
		if e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) OIDCUser(ctx context.Context, subject, name string, role model.Role) (model.User, error) {
	u, e := scanUser(s.Pool.QueryRow(ctx, "SELECT "+userCols+" FROM api_users WHERE subject=$1", subject))
	if e == nil {
		return u, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return u, e
	}
	u = model.User{ID: model.ID(), Username: name + "-" + model.ID()[:8], Subject: subject, Role: role}
	_, e = s.Pool.Exec(ctx, "INSERT INTO api_users(id,username,role,subject) VALUES($1,$2,$3,$4) ON CONFLICT(subject) DO NOTHING", u.ID, u.Username, u.Role, u.Subject)
	if e != nil {
		return u, e
	}
	return scanUser(s.Pool.QueryRow(ctx, "SELECT "+userCols+" FROM api_users WHERE subject=$1", subject))
}
func (s *Store) NewSession(ctx context.Context, u model.User) (string, string, error) {
	token, csrf := auth.Token(), auth.Token()
	_, e := s.Pool.Exec(ctx, "INSERT INTO api_sessions(id,user_id,csrf,expires) VALUES($1,$2,$3,$4)", auth.Digest(token), u.ID, csrf, time.Now().Add(24*time.Hour))
	return token, csrf, e
}
func (s *Store) Session(ctx context.Context, token string) (model.Principal, error) {
	var p model.Principal
	p.SessionID = auth.Digest(token)
	e := s.Pool.QueryRow(ctx, "SELECT u.id,u.username,u.role,s.csrf,u.display_name,u.language FROM api_sessions s JOIN api_users u ON u.id=s.user_id WHERE s.id=$1 AND s.expires>now()", p.SessionID).Scan(&p.User.ID, &p.User.Username, &p.User.Role, &p.CSRF, &p.User.Name, &p.User.Language)
	return p, e
}
func (s *Store) Logout(ctx context.Context, id string) error {
	_, e := s.Pool.Exec(ctx, "DELETE FROM api_sessions WHERE id=$1", id)
	return e
}
func (s *Store) Cameras(ctx context.Context, private bool) ([]model.Camera, error) {
	rows, e := s.Pool.Query(ctx, "SELECT config,revision FROM api_cameras ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Camera{}
	for rows.Next() {
		var b []byte
		var rev int64
		var c model.Camera
		if e = rows.Scan(&b, &rev); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &c); e != nil {
			return nil, e
		}
		c.Revision = rev
		if private {
			c.URL, e = s.cipher.Decrypt(c.URL)
			if e != nil {
				return nil, e
			}
			if c.Substream != "" {
				c.Substream, e = s.cipher.Decrypt(c.Substream)
				if e != nil {
					return nil, e
				}
			}
		} else {
			c = c.Public()
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) Camera(ctx context.Context, id string, private bool) (model.Camera, error) {
	cs, e := s.Cameras(ctx, private)
	if e != nil {
		return model.Camera{}, e
	}
	for _, c := range cs {
		if c.ID == id {
			return c, nil
		}
	}
	return model.Camera{}, ErrNotFound
}
func (s *Store) SaveCamera(ctx context.Context, c model.Camera) error {
	if e := c.Validate(); e != nil {
		return e
	}
	var e error
	c.URL, e = s.cipher.Encrypt(c.URL)
	if e != nil {
		return e
	}
	if c.Substream != "" {
		c.Substream, e = s.cipher.Encrypt(c.Substream)
		if e != nil {
			return e
		}
	}
	b, e := json.Marshal(c)
	if e != nil {
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
	if c.GroupID != "" {
		var exists bool
		e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM recording_settings,jsonb_array_elements(config->'groups') g WHERE g->>'id'=$1)", c.GroupID).Scan(&exists)
		if e != nil {
			return e
		}
		if !exists {
			return ErrNotFound
		}
	}
	if c.Revision == 0 {
		if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(712039443)"); e != nil {
			return e
		}
		tag, e := tx.Exec(ctx, "INSERT INTO api_cameras(id,config) SELECT $1,$2 WHERE (SELECT count(*) FROM api_cameras)<32 ON CONFLICT DO NOTHING", c.ID, b)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		return tx.Commit(ctx)
	}
	tag, e := tx.Exec(ctx, "UPDATE api_cameras SET config=$2,revision=revision+1 WHERE id=$1 AND revision=$3", c.ID, b, c.Revision)
	if e == nil && tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) DeleteCamera(ctx context.Context, id string) error {
	_, e := s.Pool.Exec(ctx, "DELETE FROM api_cameras WHERE id=$1", id)
	return e
}
func (s *Store) Segment(ctx context.Context, v model.Segment) error {
	_, e := s.Pool.Exec(ctx, "INSERT INTO recorder_segments(id,camera_id,shard_id,path,start_at,end_at,bytes,keep) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO UPDATE SET keep=$8", v.ID, v.CameraID, v.ShardID, v.Path, v.Start, v.End, v.Bytes, v.Keep)
	return e
}
func (s *Store) Segments(ctx context.Context, camera string, start, end time.Time) ([]model.Segment, error) {
	rows, e := s.Pool.Query(ctx, "SELECT id,camera_id,shard_id,path,start_at,end_at,bytes,keep FROM recorder_segments WHERE camera_id=$1 AND end_at>$2 AND start_at<$3 AND keep ORDER BY start_at LIMIT 10000", camera, start, end)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Segment{}
	for rows.Next() {
		var v model.Segment
		if e = rows.Scan(&v.ID, &v.CameraID, &v.ShardID, &v.Path, &v.Start, &v.End, &v.Bytes, &v.Keep); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) DeleteSegment(ctx context.Context, id string) error {
	_, e := s.Pool.Exec(ctx, "DELETE FROM recorder_segments WHERE id=$1", id)
	return e
}
func (s *Store) Event(ctx context.Context, v model.Event) (err error) {
	ctx, span := telemetry.Start(ctx, "motion persist", trace.SpanKindInternal)
	defer func() { telemetry.End(span, err) }()
	v.At = v.At.UTC().Truncate(time.Microsecond)
	v.End = v.End.UTC().Truncate(time.Microsecond)
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, "INSERT INTO recorder_events(id,camera_id,shard_id,at,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING", v.ID, v.CameraID, v.ShardID, v.At, b)
	if e != nil {
		return e
	}
	if tag.RowsAffected() > 0 {
		_, e = tx.Exec(ctx, "INSERT INTO event_outbox(id,subject,payload,trace_context) VALUES($1,'vartovyi.v1.motion',$2,$3) ON CONFLICT DO NOTHING", v.ID, b, telemetry.Carrier(ctx))
		if e != nil {
			return e
		}
	} else {
		if _, e = tx.Exec(ctx, "UPDATE recorder_events SET payload=$2 WHERE id=$1", v.ID, b); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) Events(ctx context.Context, camera string, before time.Time, limit int) ([]model.Event, error) {
	return s.EventsPage(ctx, camera, before, "~", limit)
}
func (s *Store) EventsPage(ctx context.Context, camera string, before time.Time, beforeID string, limit int) ([]model.Event, error) {
	return s.EventsOffset(ctx, camera, before, beforeID, limit, 0)
}
func (s *Store) EventsOffset(ctx context.Context, camera string, before time.Time, beforeID string, limit, offset int) ([]model.Event, error) {
	rows, e := s.Pool.Query(ctx, "SELECT payload FROM recorder_events WHERE ($1='' OR camera_id=$1) AND (at,id)<($2,$3) ORDER BY at DESC,id DESC LIMIT $4 OFFSET $5", camera, before, beforeID, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var b []byte
		var v model.Event
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Status(ctx context.Context, v model.Status) error {
	b, _ := json.Marshal(v)
	_, e := s.Pool.Exec(ctx, "INSERT INTO recorder_status(camera_id,payload) VALUES($1,$2) ON CONFLICT(camera_id) DO UPDATE SET payload=$2,updated_at=now()", v.CameraID, b)
	return e
}
func (s *Store) Statuses(ctx context.Context) ([]model.Status, error) {
	rows, e := s.Pool.Query(ctx, "SELECT payload,updated_at FROM recorder_status")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Status{}
	for rows.Next() {
		var v model.Status
		var b []byte
		var updated time.Time
		if e = rows.Scan(&b, &updated); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		if time.Since(updated) > 30*time.Second {
			v.State = "offline"
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) CreateExport(ctx context.Context, v model.Export, key string) (model.Export, error) {
	b, _ := json.Marshal(v)
	hash := auth.Digest(v.CameraID + v.Start.String() + v.End.String())
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return v, e
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, "INSERT INTO recorder_exports(id,user_id,idempotency_key,request_hash,shard_id,status,payload) VALUES($1,$2,$3,$4,$5,'pending',$6) ON CONFLICT(user_id,idempotency_key) DO NOTHING", v.ID, v.UserID, key, hash, v.ShardID, b)
	if e != nil {
		return v, e
	}
	var oldHash string
	e = tx.QueryRow(ctx, "SELECT payload,request_hash FROM recorder_exports WHERE user_id=$1 AND idempotency_key=$2", v.UserID, key).Scan(&b, &oldHash)
	if e != nil {
		return v, e
	}
	if oldHash != hash {
		return v, ErrConflict
	}
	if e = json.Unmarshal(b, &v); e != nil {
		return v, e
	}
	return v, tx.Commit(ctx)
}
func (s *Store) Exports(ctx context.Context, shard string) ([]model.Export, error) {
	rows, e := s.Pool.Query(ctx, "SELECT payload FROM recorder_exports WHERE shard_id=$1 AND status IN ('pending','running') ORDER BY created_at LIMIT 8", shard)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Export{}
	for rows.Next() {
		var v model.Export
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Export(ctx context.Context, id string) (model.Export, error) {
	var v model.Export
	var b []byte
	e := s.Pool.QueryRow(ctx, "SELECT payload FROM recorder_exports WHERE id=$1", id).Scan(&b)
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(b, &v)
	return v, e
}
func (s *Store) UpdateExport(ctx context.Context, v model.Export) error {
	b, _ := json.Marshal(v)
	_, e := s.Pool.Exec(ctx, "UPDATE recorder_exports SET payload=$2,status=$3 WHERE id=$1", v.ID, b, v.Status)
	return e
}
func (s *Store) Cleanup(ctx context.Context) error {
	_, e := s.Pool.Exec(ctx, "DELETE FROM api_sessions WHERE expires<now(); DELETE FROM notifier_deliveries WHERE updated_at<now()-interval '30 days'")
	return e
}

func (s *Store) EventsCount(ctx context.Context, camera string, before time.Time, beforeID string) (int, error) {
	var n int
	e := s.Pool.QueryRow(ctx, "SELECT count(*) FROM recorder_events WHERE ($1='' OR camera_id=$1) AND (at,id)<($2,$3)", camera, before, beforeID).Scan(&n)
	return n, e
}
