package recorder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vartovyi/internal/auth"
	"vartovyi/internal/config"
	"vartovyi/internal/media"
	"vartovyi/internal/model"
	run "vartovyi/internal/runtime"
	"vartovyi/internal/store"
	"vartovyi/internal/telemetry"
)

type worker struct {
	camera model.Camera
	cancel context.CancelFunc
}
type motion struct {
	detectorAt, lastActive time.Time
	event                  *model.Event
	traceContext           map[string]string
}

// eventManifest extends the private disk format without changing public events.
type eventManifest struct {
	model.Event
	TraceContext map[string]string `json:"trace_context,omitempty"`
}
type Recorder struct {
	Config       config.Config
	Store        *store.Store
	mu           sync.Mutex
	cameras      map[string]model.Camera
	workers      map[string]worker
	segments     map[string]model.Segment
	dirty        map[string]model.Segment
	syncedEvents map[string]bool
	context      context.Context
	ioMu         sync.Mutex
	active       map[string]bool
	motions      map[string]*motion
	lastSegment  map[string]time.Time
	lastPrune    time.Time
	ready        bool
	diskFull     bool
	mtx          media.MediaMTX
	root         *os.Root
	lock         *os.File
}
type Hook struct {
	Path     string  `json:"path"`
	Duration float64 `json:"duration"`
}
type MotionSignal = model.MotionSignal

func New(c config.Config, s *store.Store) (*Recorder, error) {
	if !model.ValidID(c.ShardID) {
		return nil, errors.New("invalid shard")
	}
	lock, e := media.Lock(c.Archive)
	if e != nil {
		return nil, e
	}
	root, e := os.OpenRoot(c.Archive)
	if e != nil {
		lock.Close()
		return nil, e
	}
	for _, d := range []string{"segments", "events", "exports", "snapshots"} {
		if e = root.MkdirAll(d, 0700); e != nil {
			root.Close()
			lock.Close()
			return nil, e
		}
	}
	r := &Recorder{Config: c, Store: s, cameras: map[string]model.Camera{}, workers: map[string]worker{}, segments: map[string]model.Segment{}, dirty: map[string]model.Segment{}, syncedEvents: map[string]bool{}, active: map[string]bool{}, motions: map[string]*motion{}, lastSegment: map[string]time.Time{}, mtx: media.MediaMTX{Base: c.MediaURL, Token: c.ServiceToken}, root: root, lock: lock}
	return r, nil
}
func (r *Recorder) Close() {
	r.mu.Lock()
	for _, w := range r.workers {
		w.cancel()
	}
	r.mu.Unlock()
	r.ioMu.Lock()
	defer r.ioMu.Unlock()
	r.root.Close()
	r.lock.Close()
}
func (r *Recorder) Run(ctx context.Context) {
	r.context = ctx
	for ctx.Err() == nil {
		if r.mtx.Reset(ctx) == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	r.recover(ctx)
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.exports(ctx)
			}
		}
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		r.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (r *Recorder) recover(ctx context.Context) {
	_ = filepath.WalkDir(filepath.Join(r.Config.Archive, "segments"), func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".mp4.json") {
			v, e := media.ReadSegmentManifest(path)
			if e == nil && model.ValidID(v.CameraID) && v.ShardID == r.Config.ShardID {
				rel, e := filepath.Rel(r.Config.Archive, strings.TrimSuffix(path, ".json"))
				if e == nil && rel == v.Path {
					if _, e = r.root.Stat(rel); e == nil {
						r.mu.Lock()
						r.segments[v.ID] = v
						r.dirty[v.ID] = v
						r.mu.Unlock()
					}
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".mp4") {
			return nil
		}
		if _, e = os.Stat(path + ".json"); e == nil {
			return nil
		}
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		duration, e := media.Probe(c, path)
		if e == nil {
			_ = r.complete(Hook{Path: path, Duration: duration}, true)
		}
		return nil
	})
	r.mu.Lock()
	r.ready = true
	r.mu.Unlock()
}

// Detection, names, groups and retention are live metadata; changing them must
// not disconnect the camera or interrupt recording.
func sameCaptureSource(a, b model.Camera) bool {
	return a.ID == b.ID && a.ShardID == b.ShardID && a.Source == b.Source && a.URL == b.URL && a.Substream == b.Substream && a.Transcode == b.Transcode && a.Transport() == b.Transport() && (!a.NeedsPublisher() || a.FPS == b.FPS)
}
func (r *Recorder) tick(ctx context.Context) {
	free, freeErr := media.DiskFree(r.Config.Archive)
	if freeErr == nil && free < 64<<20 {
		r.mu.Lock()
		r.diskFull = true
		r.mu.Unlock()
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cameras, e := r.Store.Cameras(c, true)
	dbOK := e == nil
	if dbOK {
		r.mu.Lock()
		next := map[string]model.Camera{}
		for _, camera := range cameras {
			if camera.ShardID == r.Config.ShardID {
				next[camera.ID] = camera
			}
		}
		r.cameras = next
		r.mu.Unlock()
		cipher, _ := auth.NewCipher(r.Config.EncryptionKey)
		b, _ := json.Marshal(next)
		if cipher != nil {
			encoded, e := cipher.Encrypt(string(b))
			if e == nil {
				_ = media.AtomicJSON(filepath.Join(r.Config.Archive, "cameras.enc.json"), encoded)
			}
		}
	} else {
		r.mu.Lock()
		empty := len(r.cameras) == 0
		r.mu.Unlock()
		if empty {
			var encoded string
			b, err := os.ReadFile(filepath.Join(r.Config.Archive, "cameras.enc.json"))
			cipher, ce := auth.NewCipher(r.Config.EncryptionKey)
			if err == nil && ce == nil && json.Unmarshal(b, &encoded) == nil {
				plain, err := cipher.Decrypt(encoded)
				if err == nil {
					var cached map[string]model.Camera
					if json.Unmarshal([]byte(plain), &cached) == nil {
						r.mu.Lock()
						r.cameras = cached
						r.mu.Unlock()
					}
				}
			}
		}
	}
	r.mu.Lock()
	for id, w := range r.workers {
		camera, exists := r.cameras[id]
		if !exists || !camera.Enabled || !sameCaptureSource(camera, w.camera) || r.diskFull || !r.mtx.Exists(c, id) {
			w.cancel()
			_ = r.mtx.Delete(c, id)
			_ = r.mtx.Delete(c, id+"-sub")
			delete(r.workers, id)
		}
	}
	if !r.diskFull {
		for id, camera := range r.cameras {
			if !camera.Enabled {
				continue
			}
			if _, ok := r.workers[id]; ok {
				continue
			}
			_ = r.mtx.Delete(c, id)
			_ = r.mtx.Delete(c, id+"-sub")
			if e := r.mtx.Set(c, camera); e != nil {
				continue
			}
			if e := r.mtx.Substream(c, camera); e != nil {
				_ = r.mtx.Delete(c, id)
				continue
			}
			workerCtx, workerCancel := context.WithCancel(ctx)
			r.workers[id] = worker{camera, workerCancel}
			if camera.NeedsPublisher() {
				go r.ingest(workerCtx, camera)
			}
		}
	}
	r.mu.Unlock()
	r.finishEvents(c)
	if dbOK {
		r.syncMetadata(c)
		if time.Since(r.lastPrune) > 30*time.Second {
			r.prune(c)
			r.lastPrune = time.Now()
		}
	}
	r.status(c)
	if dbOK {
		r.expireAssets(c)
	}
}
func (r *Recorder) ingest(ctx context.Context, c model.Camera) {
	for {
		if ctx.Err() != nil {
			return
		}
		_ = media.Run(ctx, media.IngestArgs(c, r.rtsp(c.ID))...)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
func (r *Recorder) relative(path string) (string, error) {
	rel, e := filepath.Rel(r.Config.Archive, path)
	if e != nil || !filepath.IsLocal(rel) || !strings.HasPrefix(filepath.ToSlash(rel), "segments/") {
		return "", errors.New("invalid segment path")
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || !model.ValidID(parts[1]) || !strings.HasSuffix(parts[2], ".mp4") {
		return "", errors.New("invalid segment identity")
	}
	return rel, nil
}
func (r *Recorder) complete(h Hook, recovery bool) error {
	if math.IsNaN(h.Duration) || math.IsInf(h.Duration, 0) || h.Duration <= 0 || h.Duration > 3600 {
		return errors.New("invalid duration")
	}
	rel, e := r.relative(h.Path)
	if e != nil {
		return e
	}
	cameraID := strings.Split(filepath.ToSlash(rel), "/")[1]
	name := strings.TrimSuffix(filepath.Base(rel), ".mp4")
	start, e := time.ParseInLocation("2006-01-02T15-04-05.000000Z0700", name, time.UTC)
	if e != nil {
		return errors.New("invalid segment timestamp")
	}
	info, e := r.root.Stat(rel)
	if e != nil || !info.Mode().IsRegular() {
		return errors.New("segment not a regular file")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cameras[cameraID]
	m := r.motions[cameraID]
	keep := recovery || !ok || !c.MotionDetectionEnabled() || c.Mode == "continuous" || m == nil || time.Since(m.detectorAt) > 10*time.Second
	end := start.Add(time.Duration(h.Duration * float64(time.Second)))
	if m != nil && m.event != nil && end.After(m.event.At.Add(-time.Duration(c.PreSeconds)*time.Second)) && start.Before(m.lastActive.Add(time.Duration(c.PostSeconds)*time.Second)) {
		keep = true
	}
	v := model.Segment{ID: auth.Digest(r.Config.ShardID + ":" + rel), CameraID: cameraID, ShardID: r.Config.ShardID, Path: rel, Start: start, End: end, Bytes: info.Size(), Keep: keep}
	if e = media.WriteSegmentManifest(r.Config.Archive, v); e != nil {
		return e
	}
	r.segments[v.ID] = v
	r.dirty[v.ID] = v
	delete(r.active, rel)
	r.lastSegment[cameraID] = end
	return nil
}
func (r *Recorder) signal(ctx context.Context, v MotionSignal) error {
	if !model.ValidID(v.CameraID) || v.Score < 0 || v.Score > 1 || math.IsNaN(v.Score) {
		return errors.New("invalid signal")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	camera, ok := r.cameras[v.CameraID]
	if !ok {
		return errors.New("unknown camera")
	}
	if !camera.Enabled || !camera.MotionDetectionEnabled() {
		return nil
	}
	m := r.motions[v.CameraID]
	if m == nil {
		m = &motion{}
		r.motions[v.CameraID] = m
	}
	now := time.Now().UTC()
	m.detectorAt = now
	if !v.Active {
		return nil
	}
	m.lastActive = now
	if m.event == nil {
		m.traceContext = telemetry.Carrier(ctx)
		m.event = &model.Event{ID: model.ID(), Version: 1, Type: "motion", CameraID: camera.ID, ShardID: r.Config.ShardID, At: now, Score: v.Score, Notify: camera.Notify}
		snapshot := filepath.Join(r.Config.Archive, "snapshots", m.event.ID+".jpg")
		m.event.Snapshot = m.event.ID
		go func() {
			c, cancel := context.WithTimeout(r.context, 12*time.Second)
			defer cancel()
			_ = media.Snapshot(c, r.rtsp(camera.ID), snapshot)
		}()
	}
	if v.Score > m.event.Score {
		m.event.Score = v.Score
	}
	m.event.End = now.Add(time.Duration(camera.PostSeconds) * time.Second)
	if e := media.AtomicJSON(filepath.Join(r.Config.Archive, "events", m.event.ID+".pending.json"), eventManifest{*m.event, m.traceContext}); e != nil {
		return e
	}
	for id, s := range r.segments {
		if s.CameraID == camera.ID && !s.Keep && s.End.After(m.event.At.Add(-time.Duration(camera.PreSeconds)*time.Second)) {
			s.Keep = true
			if e := media.WriteSegmentManifest(r.Config.Archive, s); e != nil {
				return e
			}
			r.segments[id] = s
			r.dirty[id] = s
		}
	}
	return nil
}
func (r *Recorder) finishEvents(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.motions {
		if m.event == nil || time.Now().Before(m.event.End) {
			continue
		}
		path := filepath.Join(r.Config.Archive, "events", m.event.ID+".json")
		if e := media.AtomicJSON(path, eventManifest{*m.event, m.traceContext}); e == nil {
			_ = os.Remove(strings.TrimSuffix(path, ".json") + ".pending.json")
			m.event = nil
		}
	}
	_ = ctx
}
func (r *Recorder) syncMetadata(ctx context.Context) {
	r.mu.Lock()
	segments := make([]model.Segment, 0, len(r.dirty))
	for _, s := range r.dirty {
		segments = append(segments, s)
	}
	r.mu.Unlock()
	for _, s := range segments {
		if e := r.Store.Segment(ctx, s); e != nil {
			return
		}
		r.mu.Lock()
		if current, ok := r.dirty[s.ID]; ok && current == s {
			delete(r.dirty, s.ID)
		}
		r.mu.Unlock()
	}
	entries, _ := os.ReadDir(filepath.Join(r.Config.Archive, "events"))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(r.Config.Archive, "events", entry.Name())
		if r.syncedEvents[path] {
			continue
		}
		b, e := os.ReadFile(path)
		var manifest eventManifest
		if e != nil || json.Unmarshal(b, &manifest) != nil {
			continue
		}
		v := manifest.Event
		pending := strings.HasSuffix(entry.Name(), ".pending.json")
		active := false
		if pending {
			r.mu.Lock()
			if m := r.motions[v.CameraID]; m != nil && m.event != nil && m.event.ID == v.ID {
				active = true
				v = *m.event
				manifest.TraceContext = m.traceContext
			}
			r.mu.Unlock()
		}
		if pending && time.Now().Before(v.At.Add(8*time.Second)) {
			continue
		}
		if pending && !active && time.Now().After(v.End) {
			final := strings.TrimSuffix(path, ".pending.json") + ".json"
			if media.AtomicJSON(final, eventManifest{v, manifest.TraceContext}) != nil {
				continue
			}
			_ = os.Remove(path)
			path = final
			pending = false
		}
		if e = r.Store.Event(telemetry.Extract(ctx, manifest.TraceContext), v); e != nil {
			return
		}
		if !pending {
			r.syncedEvents[path] = true
		}
	}
}
func (r *Recorder) exports(ctx context.Context) {
	r.ioMu.Lock()
	defer r.ioMu.Unlock()
	jobs, e := r.Store.Exports(ctx, r.Config.ShardID)
	if e != nil {
		return
	}
	for _, job := range jobs {
		job.Status = "running"
		if r.Store.UpdateExport(ctx, job) != nil {
			return
		}
		segments, e := r.Store.Segments(ctx, job.CameraID, job.Start, job.End)
		if e != nil {
			return
		}
		for i := range segments {
			segments[i].Path = filepath.Join(r.Config.Archive, segments[i].Path)
		}
		dest := filepath.Join(r.Config.Archive, "exports", job.ID+".mp4")
		c, cancel := context.WithTimeout(ctx, 5*time.Minute)
		e = media.Export(c, segments, job.Start, job.End, dest+".tmp.mp4")
		cancel()
		if e == nil {
			e = os.Rename(dest+".tmp.mp4", dest)
		}
		if e != nil {
			job.Status = "failed"
			job.Error = "export failed or interval contains gaps"
			_ = os.Remove(dest + ".tmp.mp4")
		} else {
			job.Status = "ready"
			job.Path = job.ID + ".mp4"
		}
		_ = r.Store.UpdateExport(ctx, job)
	}
}
func (r *Recorder) prune(ctx context.Context) {
	if !r.ioMu.TryLock() {
		return
	}
	defer r.ioMu.Unlock()
	release, e := r.Store.LockRetention(ctx)
	if e != nil {
		return
	}
	defer release()
	settings, e := r.Store.Recording(ctx)
	if e != nil {
		return
	}
	cameras, e := r.Store.Cameras(ctx, false)
	if e != nil {
		return
	}
	// Persist fail-open motion retention before evaluating shared quotas.
	r.mu.Lock()
	for id, segment := range r.segments {
		c, ok := r.cameras[segment.CameraID]
		m := r.motions[segment.CameraID]
		if ok && c.Mode == "motion" && !segment.Keep && (!c.MotionDetectionEnabled() || m == nil || time.Since(m.detectorAt) > 10*time.Second) {
			segment.Keep = true
			if media.WriteSegmentManifest(r.Config.Archive, segment) != nil {
				r.mu.Unlock()
				return
			}
			r.segments[id] = segment
			r.dirty[id] = segment
			if r.Store.Segment(ctx, segment) != nil {
				r.mu.Unlock()
				return
			}
		}
	}
	r.mu.Unlock()
	candidates, e := r.Store.RetentionCandidates(ctx, settings, cameras, r.Config.ShardID)
	if e != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	list := make([]model.Segment, 0, len(r.segments))
	var total int64
	for _, s := range r.segments {
		list = append(list, s)
		total += s.Bytes
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Start.Before(list[j].Start) })
	for _, s := range list {
		c, ok := r.cameras[s.CameraID]
		if !ok || r.active[s.Path] {
			continue
		}
		m := r.motions[s.CameraID]
		if c.Mode == "motion" && (!c.MotionDetectionEnabled() || m == nil || now.Sub(m.detectorAt) > 10*time.Second) && !s.Keep {
			s.Keep = true
			if media.WriteSegmentManifest(r.Config.Archive, s) != nil {
				continue
			}
			r.segments[s.ID] = s
			r.dirty[s.ID] = s
		}
		buffer := time.Duration(60+c.PreSeconds+c.PostSeconds+20) * time.Second
		if now.Sub(s.End) < buffer {
			continue
		}
		over := r.Config.QuotaBytes > 0 && total > r.Config.QuotaBytes
		if !s.Keep || candidates[s.ID] || over {
			if e := r.root.Remove(s.Path); e != nil && !os.IsNotExist(e) {
				continue
			}
			_ = r.root.Remove(s.Path + ".json")
			delete(r.segments, s.ID)
			delete(r.dirty, s.ID)
			total -= s.Bytes
			_ = r.Store.DeleteSegment(ctx, s.ID)
		}
	}
	free, e := media.DiskFree(r.Config.Archive)
	r.diskFull = (r.Config.QuotaBytes > 0 && total >= r.Config.QuotaBytes) || (e == nil && free < 128<<20)
}
func (r *Recorder) status(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, c := range r.cameras {
		v := model.Status{CameraID: id, ShardID: r.Config.ShardID, LastSegment: r.lastSegment[id], State: "recording"}
		if !c.Enabled {
			v.State = "disabled"
		} else if r.diskFull {
			v.State = "disk_full"
		} else if time.Since(v.LastSegment) > 40*time.Second {
			v.State = "offline"
		}
		if m := r.motions[id]; m != nil {
			v.DetectorAt = m.detectorAt
		}
		if v.State == "recording" && c.MotionDetectionEnabled() && time.Since(v.DetectorAt) > 10*time.Second {
			v.State = "detector_degraded"
		}
		_ = r.Store.Status(ctx, v)
	}
}

func (r *Recorder) Handler() http.Handler {
	m := http.NewServeMux()
	run.Health(m, func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if !r.ready {
			return errors.New("recovering")
		}
		return nil
	})
	m.HandleFunc("GET /internal/cameras", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		out := []model.Camera{}
		for _, c := range r.cameras {
			if !c.Enabled || !c.MotionDetectionEnabled() {
				continue
			}
			c.URL = r.rtsp(c.ID)
			if c.Substream != "" {
				c.Substream = r.rtsp(c.ID + "-sub")
			}
			out = append(out, c)
		}
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	m.HandleFunc("POST /internal/motion", func(w http.ResponseWriter, req *http.Request) {
		var v MotionSignal
		if json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&v) != nil || r.signal(req.Context(), v) != nil {
			http.Error(w, "invalid motion signal", 400)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("POST /internal/hooks/{kind}", func(w http.ResponseWriter, req *http.Request) {
		var h Hook
		if json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&h) != nil {
			http.Error(w, "invalid hook", 400)
			return
		}
		rel, e := r.relative(h.Path)
		if e != nil {
			http.Error(w, "invalid path", 400)
			return
		}
		switch req.PathValue("kind") {
		case "create":
			r.mu.Lock()
			r.active[rel] = true
			r.mu.Unlock()
		case "complete":
			if e = r.complete(h, false); e != nil {
				slog.Warn("segment finalization failed", "reason", e.Error())
				http.Error(w, "finalization failed", 503)
				return
			}
		default:
			http.NotFound(w, req)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("GET /media/{camera}/{kind}/{rest...}", r.serveMedia)
	m.HandleFunc("GET /files/{kind}/{id}", r.serveFile)
	m.HandleFunc("GET /metrics", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		fmt.Fprintf(w, "vartovyi_archive_segments %d\nvartovyi_disk_full %d\nvartovyi_pending_index_segments %d\n", len(r.segments), boolInt(r.diskFull), len(r.dirty))
		for id, c := range r.cameras {
			if !c.Enabled {
				continue
			}
			last := r.lastSegment[id]
			stamp := int64(0)
			if !last.IsZero() {
				stamp = last.Unix()
			}
			fmt.Fprintf(w, "vartovyi_last_segment_seconds{camera=%q,shard=%q} %d\n", id, r.Config.ShardID, stamp)
			detector := int64(0)
			if m := r.motions[id]; m != nil && !m.detectorAt.IsZero() {
				detector = m.detectorAt.Unix()
			}
			fmt.Fprintf(w, "vartovyi_detector_heartbeat_seconds{camera=%q,shard=%q} %d\n", id, r.Config.ShardID, detector)
		}
	})
	internal := run.Internal(r.Config.ServiceToken, m)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/internal/media-auth" && req.Method == "POST" {
			var v struct {
				Token    string `json:"token"`
				Password string `json:"password"`
				User     string `json:"user"`
			}
			if json.NewDecoder(io.LimitReader(req.Body, 8192)).Decode(&v) == nil && (auth.Equal(v.Token, r.Config.ServiceToken) || (v.User == "service" && auth.Equal(v.Password, r.Config.ServiceToken))) {
				w.WriteHeader(204)
				return
			}
			http.Error(w, "unauthorized", 401)
			return
		}
		internal.ServeHTTP(w, req)
	})
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func (r *Recorder) serveMedia(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("camera")
	if !model.ValidID(id) {
		http.NotFound(w, req)
		return
	}
	r.mu.Lock()
	_, exists := r.cameras[id]
	r.mu.Unlock()
	if !exists {
		http.NotFound(w, req)
		return
	}
	var target *url.URL
	switch req.PathValue("kind") {
	case "live":
		rest := req.PathValue("rest")
		if rest == "" || strings.Contains(rest, "..") || strings.ContainsAny(rest, "\\\r\n") {
			http.NotFound(w, req)
			return
		}
		target, _ = url.Parse(r.Config.HLSURL + "/" + id + "/" + rest)
		target.RawQuery = req.URL.RawQuery
	case "playback":
		target, _ = url.Parse(r.Config.PlaybackURL + "/get")
		q := req.URL.Query()
		q.Set("path", id)
		target.RawQuery = q.Encode()
	default:
		http.NotFound(w, req)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(rq *http.Request) {
		rq.URL.Scheme = target.Scheme
		rq.URL.Host = target.Host
		rq.URL.Path = target.Path
		rq.URL.RawQuery = target.RawQuery
		rq.Header.Del("Cookie")
		if req.PathValue("kind") == "live" {
			rq.Header.Set("Authorization", "Bearer "+r.Config.ServiceToken)
		} else {
			rq.SetBasicAuth("service", r.Config.ServiceToken)
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) { http.Error(w, "media unavailable", 503) }
	proxy.ServeHTTP(w, req)
}
func (r *Recorder) serveFile(w http.ResponseWriter, req *http.Request) {
	id, kind := req.PathValue("id"), req.PathValue("kind")
	if !model.ValidID(id) || (kind != "snapshots" && kind != "exports") {
		http.NotFound(w, req)
		return
	}
	ext := ".jpg"
	if kind == "exports" {
		ext = ".mp4"
	}
	f, e := r.root.Open(kind + "/" + id + ext)
	if e != nil {
		http.NotFound(w, req)
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		http.NotFound(w, req)
		return
	}
	if kind == "exports" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.mp4"`)
	}
	http.ServeContent(w, req, id+ext, info.ModTime(), f)
}

func (r *Recorder) rtsp(id string) string {
	u, _ := url.Parse(r.Config.RTSPURL + "/" + id)
	u.User = url.UserPassword("service", r.Config.ServiceToken)
	return u.String()
}

func (r *Recorder) expireAssets(ctx context.Context) {
	if !r.ioMu.TryLock() {
		return
	}
	defer r.ioMu.Unlock()
	settings, e := r.Store.Recording(ctx)
	if e != nil {
		return
	}
	now := time.Now()
	entries, _ := os.ReadDir(filepath.Join(r.Config.Archive, "events"))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".pending.json") {
			continue
		}
		path := filepath.Join(r.Config.Archive, "events", entry.Name())
		b, e := os.ReadFile(path)
		var v model.Event
		if e != nil || json.Unmarshal(b, &v) != nil || !model.ValidID(v.ID) {
			continue
		}
		r.mu.Lock()
		c, ok := r.cameras[v.CameraID]
		r.mu.Unlock()
		days := 14
		if ok {
			days = settings.AgeDays(c)
		}
		if days == 0 || now.Sub(v.End) < time.Duration(days)*24*time.Hour {
			continue
		}
		if !r.syncedEvents[path] {
			continue
		}
		if _, e = r.Store.Pool.Exec(ctx, "DELETE FROM recorder_events WHERE id=$1", v.ID); e != nil {
			return
		}
		_ = r.root.Remove("snapshots/" + v.ID + ".jpg")
		_ = os.Remove(path)
		delete(r.syncedEvents, path)
	}
	exports, _ := os.ReadDir(filepath.Join(r.Config.Archive, "exports"))
	for _, entry := range exports {
		if !strings.HasSuffix(entry.Name(), ".mp4") {
			continue
		}
		info, e := entry.Info()
		if e != nil || now.Sub(info.ModTime()) < 24*time.Hour {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".mp4")
		if !model.ValidID(id) {
			continue
		}
		v, e := r.Store.Export(ctx, id)
		if e != nil || v.Status != "ready" {
			continue
		}
		v.Status = "expired"
		if r.Store.UpdateExport(ctx, v) == nil {
			_ = r.root.Remove("exports/" + entry.Name())
		}
	}
}
