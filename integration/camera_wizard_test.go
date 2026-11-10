package integration

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	"vartovyi/internal/model"
)

func TestCameraWizardAndManualEntry(t *testing.T) {
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
	for _, path := range []string{"/cameras/new", "/cameras/new?mode=manual"} {
		r, e := admin.Get(base + path)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		page := string(b)
		if r.StatusCode != 200 {
			t.Fatal("camera setup failed")
		}
		wizard := path == "/cameras/new"
		if strings.Contains(page, `id="camera-wizard"`) != wizard || strings.Contains(page, `data-camera-form="true"`) == wizard {
			t.Fatal("wizard/manual separation failed")
		}
		if wizard && (!strings.Contains(page, `data-wizard-step="3"`) || !strings.Contains(page, `name="motion_enabled"`) || !strings.Contains(page, `id="wizard-profiles"`)) {
			t.Fatal("wizard sections missing")
		}
	}
	// Wizard uses the public camera API, including for managers. Starting disabled
	// avoids contacting even a synthetic source during this setup/permission test.
	request(t, admin, "POST", base+"/api/v1/users", me.CSRF, map[string]any{"username": "wizard-manager", "name": "Wizard manager", "password": "wizard-test-password", "role": model.Manager}, 204, nil)
	manager, managerMe := loginForTest(t, base, "wizard-manager", "wizard-test-password")
	checkPage(t, manager, base+"/cameras/new", 200)
	c := model.DefaultCamera()
	c.ID = "wizard-camera"
	c.Name = "Wizard ESP32 preset"
	c.Source = "rtsp"
	c.URL = "rtsp://source:8554/mjpeg-test"
	c.Transcode = true
	c.RTSPTransport = "tcp"
	c.Enabled = false
	c.MotionFullFrame = true
	on := true
	c.MotionEnabled = &on
	var saved model.Camera
	request(t, manager, "POST", base+"/api/v1/cameras", managerMe.CSRF, c, 200, &saved)
	if !saved.Transcode || !saved.MotionFullFrame || !saved.MotionDetectionEnabled() || saved.URL != "" {
		t.Fatal("wizard camera settings or URL redaction failed")
	}
}
