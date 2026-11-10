package integration

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"
	"vartovyi/internal/model"
)

func TestBackgroundMotionToggleAndFullFrame(t *testing.T) {
	if os.Getenv("VARTOVYI_INTEGRATION") != "1" {
		t.Skip("requires disposable Compose services")
	}
	base := os.Getenv("VARTOVYI_TEST_URL")
	wait(t, 60*time.Second, func() bool {
		r, err := http.Get(base + "/readyz")
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	})
	client, me := loginForTest(t, base, "admin", "admin")
	c := model.DefaultCamera()
	c.ID, c.Name, c.Source = "motion-toggle-camera", "Synthetic motion toggle", "rtsp"
	c.URL = "rtsp://source:8554/synthetic"
	c.Threshold = .001
	c.Zones = []model.Zone{{Name: "Exclude entire image", Exclude: true, Points: []model.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}, {X: 0, Y: 1}}}}
	request(t, client, "POST", base+"/api/v1/cameras", me.CSRF, c, 200, &c)
	defer func() {
		request(t, client, "GET", base+"/api/v1/cameras/"+c.ID, "", nil, 200, &c)
		c.Enabled = false
		request(t, client, "PUT", base+"/api/v1/cameras/"+c.ID, me.CSRF, c, 200, nil)
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=10", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-tune", "zerolatency", "-g", "20", "-f", "rtsp", "-rtsp_transport", "tcp", "rtsp://source:8554/synthetic")
	defer cancel()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	status := func() model.Status {
		var out struct {
			Items []model.Status `json:"items"`
		}
		request(t, client, "GET", base+"/api/v1/status", "", nil, 200, &out)
		for _, s := range out.Items {
			if s.CameraID == c.ID {
				return s
			}
		}
		return model.Status{}
	}
	wait(t, 35*time.Second, func() bool { return !status().DetectorAt.IsZero() })
	var events struct {
		Items []model.Event `json:"items"`
	}
	request(t, client, "GET", base+"/api/v1/events?camera_id="+c.ID, "", nil, 200, &events)
	if len(events.Items) != 0 {
		t.Fatal("excluded entire frame generated motion")
	}
	setMotion := func(enabled, full bool) {
		request(t, client, "GET", base+"/api/v1/cameras/"+c.ID, "", nil, 200, &c)
		request(t, client, "POST", base+"/ui/zones/"+c.ID, me.CSRF, map[string]any{"revision": c.Revision, "zones": c.Zones, "threshold": c.Threshold, "motion_enabled": enabled, "motion_full_frame": full}, 200, nil)
	}
	setMotion(false, false)
	disabledAt := time.Now()
	wait(t, 35*time.Second, func() bool {
		s := status()
		return !s.DetectorAt.IsZero() && time.Since(s.DetectorAt) > 12*time.Second && s.State == "recording"
	})
	// Recording must still finalize segments while no background detector runs.
	wait(t, 30*time.Second, func() bool {
		var out struct {
			Items []model.Segment `json:"items"`
		}
		q := url.Values{"start": {disabledAt.UTC().Format(time.RFC3339)}, "end": {time.Now().UTC().Format(time.RFC3339)}}
		request(t, client, "GET", base+"/api/v1/cameras/"+c.ID+"/timeline?"+q.Encode(), "", nil, 200, &out)
		for _, segment := range out.Items {
			if segment.Start.After(disabledAt) {
				return true
			}
		}
		return false
	})
	setMotion(true, true)
	enabledAt := time.Now()
	// No live page is opened: full-frame detection runs in the background and
	// overrides the retained full-image exclusion mask.
	wait(t, 35*time.Second, func() bool {
		request(t, client, "GET", base+"/api/v1/events?camera_id="+c.ID, "", nil, 200, &events)
		for _, event := range events.Items {
			if event.Type == "motion" && event.At.After(enabledAt) {
				return true
			}
		}
		return false
	})
	request(t, client, "GET", base+"/api/v1/cameras/"+c.ID, "", nil, 200, &c)
	if len(c.Zones) != 1 || !c.MotionFullFrame || !c.MotionDetectionEnabled() {
		t.Fatal("full-frame selection discarded saved zones")
	}
}
