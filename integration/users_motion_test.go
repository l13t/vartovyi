package integration

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"vartovyi/internal/model"
)

func TestUsersBulkDeletionAndMotionSettings(t *testing.T) {
	if os.Getenv("VARTOVYI_INTEGRATION") != "1" {
		t.Skip("requires disposable Compose services")
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
	ids := []string{}
	for _, role := range []model.Role{model.Viewer, model.Manager, model.Admin} {
		id := model.ID()
		ids = append(ids, id)
		request(t, admin, "POST", base+"/api/v1/users", me.CSRF, map[string]any{"id": id, "username": "bulk-" + string(role), "name": "Bulk " + string(role), "role": role, "password": "bulk-password-for-test"}, 204, nil)
	}
	viewer, viewerMe := loginForTest(t, base, "bulk-viewer", "bulk-password-for-test")
	manager, managerMe := loginForTest(t, base, "bulk-manager", "bulk-password-for-test")
	for _, principal := range []struct {
		client *http.Client
		csrf   string
	}{{viewer, viewerMe.CSRF}, {manager, managerMe.CSRF}} {
		request(t, principal.client, "POST", base+"/api/v1/users/bulk-delete", principal.csrf, map[string]any{"ids": ids}, 403, nil)
		checkPage(t, principal.client, base+"/users/new", 403)
	}
	checkPage(t, admin, base+"/users/new", 200)
	checkPage(t, admin, base+"/users/"+ids[0], 200)
	page, e := admin.Get(base + "/users?q=team%3A%20manager")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(b), "bulk-manager") || strings.Contains(string(b), "bulk-viewer") {
		t.Fatal("user role tab did not filter")
	}
	for _, tt := range []struct {
		q     string
		count int
	}{{"bulk", 3}, {"team: admin user: bulk*", 1}, {"user: bulk-v*", 1}} {
		var results struct {
			Items []model.User `json:"items"`
		}
		request(t, admin, "GET", base+"/api/v1/users?q="+url.QueryEscape(tt.q), "", nil, 200, &results)
		if len(results.Items) != tt.count {
			t.Fatalf("search %s: %d", tt.q, len(results.Items))
		}
	}
	request(t, admin, "GET", base+"/api/v1/users?q=team:", "", nil, 400, nil)
	camera := model.DefaultCamera()
	camera.ID = "motion-settings-camera"
	camera.Name = "Motion settings test camera"
	camera.Source = "rtsp"
	camera.URL = "rtsp://source:8554/synthetic"
	camera.Enabled = false
	points := []model.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}}
	camera.Zones = []model.Zone{{Name: "Двір", Points: points}, {Name: "Вікно", Exclude: true, Points: points}}
	request(t, admin, "POST", base+"/api/v1/cameras", me.CSRF, camera, 200, &camera)
	motionURL := base + "/cameras/" + camera.ID + "/motion"
	checkPage(t, viewer, motionURL, 403)
	checkPage(t, manager, motionURL, 200)
	page, e = admin.Get(motionURL)
	if e != nil {
		t.Fatal(e)
	}
	b, _ = io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(b), ">Двір</strong>") || !strings.Contains(string(b), ">Вікно</strong>") || !strings.Contains(string(b), `id="zone-list"`) {
		t.Fatal("saved zones not listed")
	}
	if !strings.Contains(string(b), `id="motion-enabled" checked`) || !strings.Contains(string(b), `id="motion-scope"`) {
		t.Fatal("motion enable and area controls missing")
	}
	page, e = admin.Get(base + "/cameras/" + camera.ID + "/settings")
	if e != nil {
		t.Fatal(e)
	}
	b, _ = io.ReadAll(page.Body)
	page.Body.Close()
	if strings.Contains(string(b), `id="zones-editor"`) {
		t.Fatal("motion settings must be separate")
	}
	changed := map[string]any{"revision": camera.Revision, "zones": []model.Zone{{Name: "Новий двір", Points: points}}, "threshold": 0.05, "pre_seconds": 3, "post_seconds": 7}
	request(t, manager, "POST", base+"/ui/zones/"+camera.ID, managerMe.CSRF, changed, 200, nil)
	request(t, admin, "POST", base+"/ui/zones/"+camera.ID, me.CSRF, changed, 409, nil)
	request(t, admin, "GET", base+"/api/v1/cameras/"+camera.ID, "", nil, 200, &camera)
	if len(camera.Zones) != 1 || camera.Zones[0].Name != "Новий двір" || camera.PreSeconds != 3 || camera.PostSeconds != 7 || camera.Threshold != .05 {
		t.Fatal("motion settings not persisted")
	}
	changed["revision"] = camera.Revision
	changed["motion_enabled"] = false
	changed["motion_full_frame"] = true
	request(t, admin, "POST", base+"/ui/zones/"+camera.ID, me.CSRF, changed, 200, nil)
	request(t, admin, "GET", base+"/api/v1/cameras/"+camera.ID, "", nil, 200, &camera)
	if camera.MotionDetectionEnabled() || !camera.MotionFullFrame || len(camera.Zones) != 1 {
		t.Fatal("motion switch or area not persisted, or zones discarded")
	}
	changed["revision"] = camera.Revision
	changed["zones"] = []model.Zone{{Name: "Invalid", Points: points[:2]}}
	request(t, admin, "POST", base+"/ui/zones/"+camera.ID, me.CSRF, changed, 400, nil)
	// Every invalid batch is atomic; no selected account may disappear.
	for _, batch := range []struct {
		ids    []string
		status int
	}{{[]string{ids[0], me.User.ID}, 400}, {[]string{ids[0], "missing-user"}, 404}, {[]string{ids[0], ids[0]}, 400}, {nil, 400}} {
		request(t, admin, "POST", base+"/api/v1/users/bulk-delete", me.CSRF, map[string]any{"ids": batch.ids}, batch.status, nil)
		request(t, viewer, "GET", base+"/api/v1/me", "", nil, 200, nil)
	}
	var deleted struct {
		Deleted int `json:"deleted"`
	}
	request(t, admin, "POST", base+"/api/v1/users/bulk-delete", me.CSRF, map[string]any{"ids": ids}, 200, &deleted)
	if deleted.Deleted != 3 {
		t.Fatal("incorrect bulk deletion count")
	}
	request(t, viewer, "GET", base+"/api/v1/me", "", nil, 401, nil)
	request(t, manager, "GET", base+"/api/v1/me", "", nil, 401, nil)
	request(t, admin, "GET", base+"/api/v1/cameras/"+camera.ID, "", nil, 200, &camera)
	checkPage(t, admin, base+"/users", 200)
}
