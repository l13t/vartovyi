package media

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"vartovyi/internal/model"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMediaControlAuthentication(t *testing.T) {
	m := MediaMTX{Base: "http://media", Token: "private-service-token", Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "service" || password != "private-service-token" {
			t.Fatal("MediaMTX control must use version-compatible Basic auth")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[]}`)), Header: make(http.Header)}, nil
	})}}
	if e := m.Set(context.Background(), model.Camera{ID: "pi", Source: "h264_tcp"}); e != nil {
		t.Fatal(e)
	}
	if e := m.Reset(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestMotionMasks(t *testing.T) {
	z := model.Zone{Exclude: true, Points: []model.Point{{X: 0, Y: 0}, {X: 0.5, Y: 0}, {X: 0.5, Y: 1}, {X: 0, Y: 1}}}
	m := NewMotion(10, 10, []model.Zone{z})
	a := make([]byte, 100)
	m.Score(a)
	for y := 0; y < 10; y++ {
		for x := 0; x < 5; x++ {
			a[y*10+x] = 255
		}
	}
	if score := m.Score(a); score != 0 {
		t.Fatalf("excluded motion scored %f", score)
	}
	for i := range a {
		a[i] = 255
	}
	if m.Score(a) < 0.9 {
		t.Fatal("included motion ignored")
	}
}
func TestArchiveLock(t *testing.T) {
	dir := t.TempDir()
	f, e := Lock(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if f2, e := Lock(dir); e == nil {
		f2.Close()
		t.Fatal("two owners allowed")
	}
	if e = AtomicJSON(filepath.Join(dir, "meta", "segment.json"), model.Segment{ID: "one"}); e != nil {
		t.Fatal(e)
	}
}

func TestManifestPreservesPrivatePath(t *testing.T) {
	dir := t.TempDir()
	s := model.Segment{ID: "segment", CameraID: "pi", ShardID: "main", Path: "segments/pi/frame.mp4", Start: time.Now().UTC(), End: time.Now().UTC().Add(time.Second), Keep: true}
	if e := WriteSegmentManifest(dir, s); e != nil {
		t.Fatal(e)
	}
	got, e := ReadSegmentManifest(filepath.Join(dir, s.Path) + ".json")
	if e != nil || got.Path != s.Path || got.ID != s.ID || !got.Keep {
		t.Fatalf("manifest lost durable state: %+v %v", got, e)
	}
	public, e := json.Marshal(got)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(public), s.Path) {
		t.Fatal("public JSON leaked archive path")
	}
}
func TestSyntheticExport(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	input := filepath.Join(dir, "input.mp4")
	if e := Run(ctx, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=10", "-t", "3", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-g", "10", input); e != nil {
		t.Fatal(e)
	}
	duration, e := Probe(ctx, input)
	if e != nil || duration < 2.9 {
		t.Fatalf("probe %v %v", duration, e)
	}
	start := time.Now()
	dest := filepath.Join(dir, "export.mp4")
	if e = Export(ctx, []model.Segment{{Path: input, Start: start, End: start.Add(3 * time.Second)}}, start, start.Add(2*time.Second), dest); e != nil {
		t.Fatal(e)
	}
	if info, e := os.Stat(dest); e != nil || info.Size() == 0 {
		t.Fatal("missing export")
	}
}

func TestRTSPTranscodingPipeline(t *testing.T) {
	c := model.DefaultCamera()
	c.Source = "rtsp"
	c.Transcode = true
	c.RTSPTransport = "udp"
	c.URL = "rtsp://example.invalid/mjpeg/1"
	dest := "rtsp://media/output"
	args := IngestArgs(c, dest)
	joined := strings.Join(args, " ")
	// FFmpeg 7+ rejects an output frame-rate override with VFR. Preserve the
	// receive timestamps rather than synthesizing duplicate catch-up frames.
	if strings.Contains(strings.Join(IngestArgs(c, dest), " "), " -r ") {
		t.Fatal("VFR ingest must not force output frame rate")
	}
	for _, want := range []string{"-rtsp_transport udp", "-timeout 10000000", "-use_wallclock_as_timestamps 1", "setpts=PTS-STARTPTS", "-fps_mode vfr", "-force_key_frames expr:gte(t,n_forced*2)", "-c:v libx264", "format=yuv420p", "-threads 2", "-rtsp_transport tcp " + dest} {
		if !strings.Contains(joined, want) {
			t.Fatal("transcode pipeline missing: " + want)
		}
	}
	m := MediaMTX{Base: "http://media", Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		var cfg map[string]any
		if e := json.NewDecoder(r.Body).Decode(&cfg); e != nil {
			t.Fatal(e)
		}
		if cfg["source"] != "publisher" {
			t.Fatal("MJPEG must not bypass the encoder")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}}
	if e := m.Set(context.Background(), c); e != nil {
		t.Fatal(e)
	}
}

func TestFullFrameMotionOverridesSavedMasks(t *testing.T) {
	c := model.DefaultCamera()
	c.Zones = []model.Zone{{Name: "Left", Points: []model.Point{{X: 0, Y: 0}, {X: .4, Y: 0}, {X: .4, Y: 1}, {X: 0, Y: 1}}}}
	black := make([]byte, 100)
	right := make([]byte, 100)
	for y := 0; y < 10; y++ {
		for x := 5; x < 10; x++ {
			right[y*10+x] = 255
		}
	}
	limited := NewMotion(10, 10, c.MotionZones())
	limited.Score(black)
	if limited.Score(right) != 0 {
		t.Fatal("zone mode must ignore motion outside saved polygon")
	}
	c.MotionFullFrame = true
	full := NewMotion(10, 10, c.MotionZones())
	full.Score(black)
	if full.Score(right) < .4 {
		t.Fatal("full-frame mode must detect motion outside saved zones")
	}
	if len(c.Zones) != 1 {
		t.Fatal("full-frame mode must preserve saved zones")
	}
}
