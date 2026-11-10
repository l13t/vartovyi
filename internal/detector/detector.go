package detector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
	"vartovyi/internal/config"
	"vartovyi/internal/media"
	"vartovyi/internal/model"
	run "vartovyi/internal/runtime"
	"vartovyi/internal/telemetry"
)

type Detector struct {
	Config config.Config
	client *http.Client
	mu     sync.Mutex
	active int
	ready  bool
}

func New(c config.Config) *Detector {
	return &Detector{Config: c, client: &http.Client{Timeout: 8 * time.Second, Transport: telemetry.Transport{Propagate: true}}}
}
func (d *Detector) Run(ctx context.Context) {
	type worker struct {
		revision int64
		cancel   context.CancelFunc
	}
	workers := map[string]worker{}
	defer func() {
		for _, w := range workers {
			w.cancel()
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		var cameras []model.Camera
		if d.request(ctx, "GET", "/internal/cameras", nil, &cameras) == nil {
			found := map[string]bool{}
			for _, c := range cameras {
				if !c.MotionDetectionEnabled() {
					continue
				}
				found[c.ID] = true
				if w, ok := workers[c.ID]; ok && w.revision == c.Revision {
					continue
				} else if ok {
					w.cancel()
				}
				workerCtx, cancel := context.WithCancel(ctx)
				workers[c.ID] = worker{c.Revision, cancel}
				go d.camera(workerCtx, c)
			}
			for id, w := range workers {
				if !found[id] {
					w.cancel()
					delete(workers, id)
				}
			}
			d.mu.Lock()
			d.active = len(workers)
			d.ready = true
			d.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (d *Detector) camera(ctx context.Context, c model.Camera) {
	source := c.URL
	if c.Substream != "" {
		source = c.Substream
	}
	for ctx.Err() == nil {
		pipe, done, e := media.Stream(ctx, source)
		if e == nil {
			motion := media.NewMotion(320, 180, c.MotionZones())
			frame := make([]byte, 320*180)
			consecutive := 0
			for {
				if _, e = io.ReadFull(pipe, frame); e != nil {
					break
				}
				score := motion.Score(frame)
				if score >= c.Threshold {
					consecutive++
				} else {
					consecutive = 0
				}
				signal := model.MotionSignal{CameraID: c.ID, Score: score, Active: consecutive >= 2}
				if e = d.request(ctx, "POST", "/internal/motion", signal, nil); e != nil {
					break
				}
			}
			done()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
func (d *Detector) request(ctx context.Context, method, path string, value, out any) error {
	var body io.Reader
	if value != nil {
		b, e := json.Marshal(value)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, d.Config.RecorderURL+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+d.Config.ServiceToken)
	req.Header.Set("Content-Type", "application/json")
	resp, e := d.client.Do(req)
	if e != nil {
		return errors.New("recorder unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return errors.New("recorder rejected request")
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	return nil
}
func (d *Detector) Handler() http.Handler {
	m := http.NewServeMux()
	run.Health(m, func() error {
		d.mu.Lock()
		defer d.mu.Unlock()
		if !d.ready {
			return errors.New("waiting for recorder")
		}
		return nil
	})
	return run.Internal(d.Config.ServiceToken, m)
}
