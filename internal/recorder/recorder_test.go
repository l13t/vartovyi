package recorder

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"vartovyi/internal/config"
	"vartovyi/internal/model"
)

func TestFinalizationIsIdempotentAndConfined(t *testing.T) {
	dir := t.TempDir()
	r, e := New(config.Config{ShardID: "main", Archive: dir}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	r.context = context.Background()
	if e = os.MkdirAll(filepath.Join(dir, "segments", "pi"), 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "segments", "pi", "2026-11-03T12-00-00.123456+0000.mp4")
	if e = os.WriteFile(path, []byte("synthetic segment"), 0600); e != nil {
		t.Fatal(e)
	}
	r.cameras["pi"] = model.Camera{ID: "pi", Mode: "continuous"}
	for i := 0; i < 2; i++ {
		if e = r.complete(Hook{path, 10}, false); e != nil {
			t.Fatal(e)
		}
	}
	if len(r.segments) != 1 {
		t.Fatal("duplicate manifest")
	}
	if e = r.complete(Hook{filepath.Join(dir, "..", "outside.mp4"), 10}, false); e == nil {
		t.Fatal("path escaped archive")
	}
	if e = r.complete(Hook{path, -1}, false); e == nil {
		t.Fatal("negative duration accepted")
	}
	if _, e = os.Stat(path + ".json"); e != nil {
		t.Fatal(e)
	}
}

func TestFinalizationAcceptsMediaMTXTimezones(t *testing.T) {
	for _, zone := range []string{"Z", "+0000", "+0200", "-0530"} {
		t.Run(zone, func(t *testing.T) {
			dir := t.TempDir()
			r, e := New(config.Config{ShardID: "main", Archive: dir}, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			if e = os.MkdirAll(filepath.Join(dir, "segments", "pi"), 0700); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(dir, "segments", "pi", "2026-11-04T09-00-00.123456"+zone+".mp4")
			if e = os.WriteFile(path, []byte("synthetic"), 0600); e != nil {
				t.Fatal(e)
			}
			if e = r.complete(Hook{path, 10}, false); e != nil {
				t.Fatal(e)
			}
			if len(r.segments) != 1 {
				t.Fatal("missing segment")
			}
		})
	}
}

func TestDisabledDetectionIgnoresSignalsAndRetainsVideo(t *testing.T) {
	dir := t.TempDir()
	r, e := New(config.Config{ShardID: "main", Archive: dir}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	r.context = context.Background()
	off := false
	c := model.DefaultCamera()
	c.ID = "motion-disabled"
	c.Enabled = true
	c.Mode = "motion"
	c.MotionEnabled = &off
	r.cameras[c.ID] = c
	if e = r.signal(context.Background(), model.MotionSignal{CameraID: c.ID, Score: 1, Active: true}); e != nil {
		t.Fatal(e)
	}
	if len(r.motions) != 0 {
		t.Fatal("disabled detector accepted a stale signal")
	}
	r.motions[c.ID] = &motion{detectorAt: time.Now()}
	path := filepath.Join(dir, "segments", c.ID, "2026-11-05T12-00-00.123456+0000.mp4")
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("synthetic segment"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = r.complete(Hook{Path: path, Duration: 10}, false); e != nil {
		t.Fatal(e)
	}
	for _, segment := range r.segments {
		if !segment.Keep {
			t.Fatal("disabling detection must not discard motion-mode video")
		}
	}
}
func TestMotionChangesDoNotRestartCapture(t *testing.T) {
	a := model.DefaultCamera()
	a.Source = "rtsp"
	a.URL = "rtsp://fixture/camera"
	b := a
	off := false
	b.MotionEnabled = &off
	b.MotionFullFrame = true
	b.Revision++
	b.Threshold = .5
	b.Name = "Renamed"
	b.RetentionDays = 1
	if !sameCaptureSource(a, b) {
		t.Fatal("motion or metadata changes must not restart video")
	}
	b.URL = "rtsp://fixture/other"
	if sameCaptureSource(a, b) {
		t.Fatal("changing source URL must reconnect")
	}
}
