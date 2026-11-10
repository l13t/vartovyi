package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"vartovyi/internal/model"
)

func TestRecordingThroughPublicAPI(t *testing.T) {
	if os.Getenv("VARTOVYI_INTEGRATION") != "1" {
		t.Skip("run make integration against disposable services")
	}
	base := os.Getenv("VARTOVYI_TEST_URL")
	readyClient := &http.Client{Timeout: 15 * time.Second}
	wait(t, 60*time.Second, func() bool {
		resp, e := readyClient.Get(base + "/readyz")
		if e != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	})
	client, me := loginForTest(t, base, "admin", "admin")
	camera := model.DefaultCamera()
	camera.ID = "integration-camera"
	camera.Name = "Synthetic integration camera"
	camera.Source = "rtsp"
	camera.URL = "rtsp://source:8554/synthetic"
	camera.FPS = 10
	camera.Threshold = 0.001
	camera.PostSeconds = 1
	request(t, client, "POST", base+"/api/v1/cameras", me.CSRF, camera, 200, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=10", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-g", "20", "-f", "rtsp", "-rtsp_transport", "tcp", "rtsp://source:8554/synthetic")
	// No HTML page or live stream has been opened: detection must run solely
	// from the background detector service, while the viewing user is signed out.
	logout, err := client.PostForm(base+"/logout", url.Values{"csrf": {me.CSRF}})
	if err != nil {
		t.Fatal(err)
	}
	logout.Body.Close()
	loggedOutAt := time.Now()
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	time.Sleep(12 * time.Second)
	loginAt := time.Now()
	client, me = loginForTest(t, base, "admin", "admin")
	wait(t, 30*time.Second, func() bool {
		var events struct {
			Items []model.Event `json:"items"`
		}
		request(t, client, "GET", base+"/api/v1/events?camera_id="+camera.ID, "", nil, 200, &events)
		for _, event := range events.Items {
			if event.Type == "motion" && event.Score >= camera.Threshold && event.At.After(loggedOutAt) && event.At.Before(loginAt) {
				return true
			}
		}
		return false
	})
	var segments []model.Segment
	wait(t, 70*time.Second, func() bool {
		q := url.Values{"start": {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}, "end": {time.Now().UTC().Format(time.RFC3339)}}
		resp, e := client.Get(base + "/api/v1/cameras/" + camera.ID + "/timeline?" + q.Encode())
		if e != nil {
			return false
		}
		defer resp.Body.Close()
		var out struct {
			Items []model.Segment `json:"items"`
		}
		if json.NewDecoder(resp.Body).Decode(&out) != nil {
			return false
		}
		segments = out.Items
		return len(segments) > 0
	})
	checkLiveHLS(t, client, base+"/media/"+camera.ID+"/live/index.m3u8")
	wait(t, 20*time.Second, func() bool {
		var statuses struct {
			Items []model.Status `json:"items"`
		}
		request(t, client, "GET", base+"/api/v1/status", "", nil, 200, &statuses)
		for _, status := range statuses.Items {
			if status.CameraID == camera.ID && !status.DetectorAt.IsZero() && time.Since(status.DetectorAt) < 10*time.Second {
				return true
			}
		}
		return false
	})
	segment := segments[0]
	payload := map[string]any{"camera_id": camera.ID, "start": segment.Start, "end": segment.End}
	data, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", base+"/api/v1/exports", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", me.CSRF)
	req.Header.Set("Idempotency-Key", "integration-export")
	resp, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 202 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("export %d %s", resp.StatusCode, b)
	}
	var job model.Export
	if e = json.NewDecoder(resp.Body).Decode(&job); e != nil {
		t.Fatal(e)
	}
	wait(t, 30*time.Second, func() bool {
		request(t, client, "GET", base+"/api/v1/exports/"+job.ID, "", nil, 200, &job)
		if job.Status == "failed" {
			t.Fatalf("export failed: %s", job.Error)
		}
		return job.Status == "ready"
	})
	resp, e = client.Get(base + "/api/v1/exports/" + job.ID + "/download")
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if e != nil || resp.StatusCode != 200 || len(b) < 1000 {
		t.Fatal("empty MP4 download")
	}
	if !bytes.Contains(b[:min(128, len(b))], []byte("ftyp")) {
		t.Fatal("not an MP4")
	}
	request(t, client, "POST", base+"/api/v1/users", me.CSRF, map[string]any{"username": "viewer", "password": "viewer-test-password", "role": "viewer"}, 204, nil)
	viewer, viewerMe := loginForTest(t, base, "viewer", "viewer-test-password")
	request(t, viewer, "POST", base+"/api/v1/cameras", viewerMe.CSRF, camera, 403, nil)
}
func wait(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("timed out waiting for service behavior")
}
func request(t *testing.T, c *http.Client, method, path, csrf string, value any, status int, out any) {
	t.Helper()
	var body io.Reader
	if value != nil {
		b, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequest(method, path, body)
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	resp, e := c.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: want %d got %d: %s", method, strings.TrimPrefix(path, "http://"), status, resp.StatusCode, b)
	}
	if out != nil {
		if e = json.NewDecoder(resp.Body).Decode(out); e != nil {
			t.Fatal(fmt.Errorf("decode response: %w", e))
		}
	}
}

func checkLiveHLS(t *testing.T, client *http.Client, master string) {
	t.Helper()
	get := func(address string) []byte {
		t.Helper()
		resp, e := client.Get(address)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if e != nil || resp.StatusCode != 200 {
			t.Fatalf("HLS %s: status %d, error %v", address, resp.StatusCode, e)
		}
		return b
	}
	parent, _ := url.Parse(master)
	playlist := get(master)
	if !bytes.HasPrefix(playlist, []byte("#EXTM3U")) {
		t.Fatal("invalid HLS master")
	}
	child := ""
	for _, line := range strings.Split(string(playlist), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			child = line
			break
		}
	}
	if child == "" {
		t.Fatal("HLS master has no variant")
	}
	relative, e := url.Parse(child)
	if e != nil {
		t.Fatal(e)
	}
	variant := parent.ResolveReference(relative)
	media := get(variant.String())
	if !bytes.HasPrefix(media, []byte("#EXTM3U")) {
		t.Fatal("invalid HLS media playlist")
	}
	segment := ""
	skipGap := false
	for _, line := range strings.Split(string(media), "\n") {
		line = strings.TrimSpace(line)
		if line == "#EXT-X-GAP" {
			skipGap = true
			continue
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			if skipGap {
				skipGap = false
				continue
			}
			segment = line
			break
		}
	}
	if segment == "" {
		t.Fatal("HLS playlist has no complete segment")
	}
	relative, e = url.Parse(segment)
	if e != nil {
		t.Fatal(e)
	}
	if b := get(variant.ResolveReference(relative).String()); len(b) < 100 {
		t.Fatal("empty HLS media fragment")
	}
}
