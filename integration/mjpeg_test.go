package integration

import (
	"bytes"
	"context"
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

func TestMJPEGTranscodingThroughPublicAPI(t *testing.T) {
	if os.Getenv("VARTOVYI_INTEGRATION") != "1" {
		t.Skip("requires disposable services")
	}
	base := os.Getenv("VARTOVYI_TEST_URL")
	wait(t, 60*time.Second, func() bool {
		r, e := http.Get(base + "/readyz")
		if e != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	})
	admin, me := loginForTest(t, base, "admin", "admin")
	c := model.DefaultCamera()
	c.ID = "mjpeg-transcode-camera"
	c.Name = "Synthetic MJPEG"
	c.Source = "rtsp"
	c.URL = "rtsp://source:8554/mjpeg-test"
	c.Transcode = true
	c.FPS = 5
	request(t, admin, "POST", base+"/api/v1/cameras", me.CSRF, c, 200, &c)
	defer func() {
		request(t, admin, "GET", base+"/api/v1/cameras/"+c.ID, "", nil, 200, &c)
		c.Enabled = false
		c.Substream = "__unchanged__"
		request(t, admin, "PUT", base+"/api/v1/cameras/"+c.ID, me.CSRF, c, 200, nil)
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=5", "-c:v", "mjpeg", "-threads", "1", "-q:v", "5", "-pix_fmt", "yuvj420p", "-huffman", "default", "-force_duplicated_matrix", "1", "-f", "rtsp", "-rtsp_transport", "tcp", "rtsp://source:8554/mjpeg-test")
	var fixtureLog bytes.Buffer
	cmd.Stderr = &fixtureLog
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
		if t.Failed() {
			log := fixtureLog.String()
			if len(log) > 2000 {
				log = log[:2000]
			}
			t.Log("synthetic MJPEG publisher:", log)
		}
	}()
	wait(t, 70*time.Second, func() bool {
		var timeline struct {
			Items []model.Segment `json:"items"`
		}
		q := url.Values{"start": {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}, "end": {time.Now().UTC().Format(time.RFC3339)}}
		request(t, admin, "GET", base+"/api/v1/cameras/"+c.ID+"/timeline?"+q.Encode(), "", nil, 200, &timeline)
		return len(timeline.Items) > 0
	})
	checkLiveHLS(t, admin, base+"/media/"+c.ID+"/live/index.m3u8")
	r, e := admin.Get(base + "/media/" + c.ID + "/live/index.m3u8")
	if e != nil {
		t.Fatal(e)
	}
	playlist, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(playlist), "avc1") {
		t.Fatal("MJPEG must become H.264 for browser playback")
	}
	r, e = admin.Get(base + "/cameras/" + c.ID + "/settings")
	if e != nil {
		t.Fatal(e)
	}
	page, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(page), "data-stream-configured") || strings.Contains(string(page), "rtsp://source:8554/mjpeg-test") {
		t.Fatal("saved URL must be acknowledged without exposure")
	}
	// Legacy form submissions must preserve connection options and encrypted URLs.
	form := url.Values{"csrf": {me.CSRF}, "id": {c.ID}, "revision": {"1"}, "name": {c.Name}, "source": {"rtsp"}, "shard_id": {"main"}, "fps": {"5"}, "mode": {"continuous"}, "enabled": {"on"}}
	r, e = admin.PostForm(base+"/ui/cameras", form)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("legacy camera form failed")
	}
	request(t, admin, "GET", base+"/api/v1/cameras/"+c.ID, "", nil, 200, &c)
	if !c.Transcode {
		t.Fatal("legacy form reset transcoding")
	}
}
