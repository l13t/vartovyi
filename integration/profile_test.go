package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"vartovyi/internal/model"
	"vartovyi/internal/store"
)

type identity struct {
	User model.User `json:"user"`
	CSRF string     `json:"csrf_token"`
}

func loginForTest(t *testing.T, base, username, password string) (*http.Client, identity) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	loginStatus(t, client, base, username, password, 200)
	var me identity
	request(t, client, "GET", base+"/api/v1/me", "", nil, 200, &me)
	return client, me
}
func loginStatus(t *testing.T, client *http.Client, base, username, password string, want int) {
	t.Helper()
	resp, e := client.Get(base + "/login")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	match := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(b)
	if len(match) != 2 {
		t.Fatal("missing login CSRF")
	}
	for i := 0; i < 4; i++ {
		resp, e = client.PostForm(base+"/login", url.Values{"username": {username}, "password": {password}, "csrf": {string(match[1])}})
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode == 429 {
			time.Sleep(1100 * time.Millisecond)
			continue
		}
		if resp.StatusCode != want {
			t.Fatalf("login status: want %d got %d", want, resp.StatusCode)
		}
		return
	}
	t.Fatal("login remained rate limited")
}
func TestProfileSelfServiceAndPagePermissions(t *testing.T) {
	if os.Getenv("VARTOVYI_INTEGRATION") != "1" {
		t.Skip("requires disposable Compose services")
	}
	base := os.Getenv("VARTOVYI_TEST_URL")
	anonymous := &http.Client{Timeout: 3 * time.Second}
	wait(t, 60*time.Second, func() bool {
		r, e := anonymous.Get(base + "/readyz")
		if e != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	})
	admin, adminMe := loginForTest(t, base, "admin", "admin")
	for _, role := range []model.Role{model.Viewer, model.Manager} {
		request(t, admin, "POST", base+"/api/v1/users", adminMe.CSRF, map[string]any{"username": "profile-" + string(role), "name": "Початкове ім’я", "password": "original-profile-password", "role": role}, 204, nil)
	}
	viewer, me := loginForTest(t, base, "profile-viewer", "original-profile-password")
	if me.User.Role != model.Viewer || me.User.Name != "Початкове ім’я" {
		t.Fatal("unexpected initial principal")
	}
	for _, path := range []string{"/profile", "/events", "/"} {
		checkPage(t, viewer, base+path, 200)
	}
	for _, path := range []string{"/users", "/settings", "/tools/onvif", "/cameras/new", "/cameras/unused/settings"} {
		checkPage(t, viewer, base+path, 403)
	}
	for _, path := range []string{"/users", "/settings", "/cameras/new", "/tools/onvif"} {
		checkPage(t, admin, base+path, 200)
	}
	manager, _ := loginForTest(t, base, "profile-manager", "original-profile-password")
	for _, path := range []string{"/profile", "/events", "/cameras/new", "/tools/onvif"} {
		checkPage(t, manager, base+path, 200)
	}
	for _, path := range []string{"/users", "/settings"} {
		checkPage(t, manager, base+path, 403)
	}
	request(t, viewer, "PATCH", base+"/api/v1/profile", me.CSRF, map[string]any{"name": "Ірина"}, 200, &me)
	if me.User.Role != model.Viewer || me.User.Username != "profile-viewer" || me.User.Name != "Ірина" {
		t.Fatal("profile changed protected attributes")
	}
	request(t, viewer, "PATCH", base+"/api/v1/profile", me.CSRF, map[string]any{"role": "admin"}, 400, nil)
	request(t, viewer, "PATCH", base+"/api/v1/profile", me.CSRF, map[string]any{"id": adminMe.User.ID, "name": "Wrong account"}, 400, nil)
	request(t, viewer, "PATCH", base+"/api/v1/profile", me.CSRF, map[string]any{"name": "   "}, 400, nil)
	request(t, viewer, "PATCH", base+"/api/v1/profile", me.CSRF, map[string]any{"current_password": "wrong", "new_password": "changed-profile-password"}, 400, nil)
	other, _ := loginForTest(t, base, "profile-viewer", "original-profile-password")
	request(t, viewer, "PATCH", base+"/api/v1/profile", me.CSRF, map[string]any{"current_password": "original-profile-password", "new_password": "changed-profile-password"}, 200, &me)
	if me.CSRF == "" || me.User.Role != model.Viewer {
		t.Fatal("password change lost current principal")
	}
	request(t, other, "GET", base+"/api/v1/me", "", nil, 401, nil)
	request(t, viewer, "GET", base+"/api/v1/me", "", nil, 200, &me)
	jar, _ := cookiejar.New(nil)
	oldLogin := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	loginStatus(t, oldLogin, base, "profile-viewer", "original-profile-password", 401)
	loginForTest(t, base, "profile-viewer", "changed-profile-password")

	// Emulate an already authenticated OIDC principal, without claiming provider
	// integration: accounts with no local credential must not create one here.
	s, e := store.Open(t.Context(), "postgres://vartovyi:test-password@postgres:5432/vartovyi?sslmode=disable", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	camera := model.DefaultCamera()
	camera.ID = "profile-form-camera"
	camera.Name = "Form camera"
	camera.Source = "rtsp"
	camera.URL = "rtsp://source:8554/synthetic"
	camera.Substream = camera.URL
	camera.Enabled = false
	request(t, admin, "POST", base+"/api/v1/cameras", adminMe.CSRF, camera, 200, nil)
	form := url.Values{"csrf": {adminMe.CSRF}, "id": {camera.ID}, "revision": {"1"}, "name": {"Renamed form camera"}, "shard_id": {"main"}, "source": {"rtsp"}, "fps": {"10"}, "mode": {"continuous"}, "retention_days": {"14"}, "quota_bytes": {"0"}, "url": {""}, "substream": {""}}
	resp, e := admin.PostForm(base+"/ui/cameras", form)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("camera edit failed")
	}
	saved, e := s.Camera(t.Context(), camera.ID, true)
	if e != nil || saved.URL != camera.URL || saved.Substream != camera.Substream || saved.Name != "Renamed form camera" {
		t.Fatal("blank connection fields must preserve existing streams")
	}
	form.Set("revision", "2")
	form.Set("clear_substream", "on")
	resp, e = admin.PostForm(base+"/ui/cameras", form)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	saved, e = s.Camera(t.Context(), camera.ID, true)
	if e != nil || saved.Substream != "" || saved.URL != camera.URL {
		t.Fatal("explicit substream removal failed")
	}

	oidcUser, e := s.OIDCUser(t.Context(), "https://issuer.example|"+model.ID(), "oidc-profile", model.Viewer)
	if e != nil {
		t.Fatal(e)
	}
	token, csrf, e := s.NewSession(t.Context(), oidcUser)
	if e != nil {
		t.Fatal(e)
	}
	jar, _ = cookiejar.New(nil)
	origin, _ := url.Parse(base)
	jar.SetCookies(origin, []*http.Cookie{{Name: "vartovyi_session", Value: token, Path: "/"}})
	oidcClient := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	var profile struct {
		CanChange bool `json:"can_change_password"`
	}
	request(t, oidcClient, "GET", base+"/api/v1/profile", "", nil, 200, &profile)
	if profile.CanChange {
		t.Fatal("OIDC account advertised local password")
	}
	request(t, oidcClient, "PATCH", base+"/api/v1/profile", csrf, map[string]any{"name": "OIDC Ім’я"}, 200, nil)
	request(t, oidcClient, "PATCH", base+"/api/v1/profile", csrf, map[string]any{"current_password": "anything", "new_password": "not-a-local-password"}, 400, nil)
}
func checkPage(t *testing.T, c *http.Client, path string, want int) {
	t.Helper()
	r, e := c.Get(path)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != want {
		t.Fatalf("page %s: want %d got %d", path, want, r.StatusCode)
	}
	if want == 200 && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var result map[string]any
		_ = json.Unmarshal(b, &result)
		t.Fatalf("page returned JSON error: %v", result)
	}
}
