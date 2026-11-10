package integration

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"vartovyi/internal/store"
)

func TestTelegramSettingsAndLanguage(t *testing.T) {
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
	request(t, admin, "POST", base+"/api/v1/users", me.CSRF, map[string]any{"username": "settings-viewer", "password": "settings-viewer-password", "role": "viewer"}, 204, nil)
	viewer, viewerMe := loginForTest(t, base, "settings-viewer", "settings-viewer-password")
	for _, method := range []string{"GET", "PUT"} {
		request(t, viewer, method, base+"/api/v1/settings/oidc", viewerMe.CSRF, map[string]any{}, 403, nil)
		request(t, viewer, method, base+"/api/v1/settings/telegram", viewerMe.CSRF, map[string]any{}, 403, nil)
	}
	var settings map[string]any
	request(t, admin, "PUT", base+"/api/v1/settings/telegram", me.CSRF, map[string]any{"enabled": true, "chat_id": "-100123", "token": "123:synthetic_test_token"}, 200, &settings)
	if settings["token"] != nil || settings["token_set"] != true {
		t.Fatal("token must be write-only")
	}
	request(t, admin, "PUT", base+"/api/v1/settings/telegram", me.CSRF, map[string]any{"enabled": true, "chat_id": "@test_channel"}, 200, &settings)
	s, e := store.Open(t.Context(), "postgres://vartovyi:test-password@postgres:5432/vartovyi?sslmode=disable", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	saved, e := s.Telegram(t.Context(), "", "")
	if e != nil || saved.Token != "123:synthetic_test_token" || saved.ChatID != "@test_channel" {
		t.Fatal("blank token must preserve saved credential")
	}
	var encrypted string
	if e = s.Pool.QueryRow(t.Context(), "SELECT token FROM telegram_settings WHERE id=1").Scan(&encrypted); e != nil || strings.Contains(encrypted, "synthetic_test_token") {
		t.Fatal("token must be encrypted at rest")
	}
	request(t, admin, "PUT", base+"/api/v1/settings/telegram", me.CSRF, map[string]any{"enabled": true, "chat_id": "123", "token": "invalid/token"}, 400, nil)
	r, e := admin.Get(base + "/settings?tab=telegram")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if strings.Contains(string(b), "synthetic_test_token") {
		t.Fatal("HTML leaked token")
	}
	request(t, admin, "PUT", base+"/api/v1/settings/telegram", me.CSRF, map[string]any{"enabled": false, "chat_id": "", "clear_token": true}, 200, &settings)
	saved, e = s.Telegram(t.Context(), "999:fallback", "999")
	if e != nil || saved.Enabled || saved.Token != "" {
		t.Fatal("explicit disable must override config fallback")
	}
	request(t, viewer, "PATCH", base+"/api/v1/profile", viewerMe.CSRF, map[string]any{"language": "en"}, 200, nil)
	r, e = viewer.Get(base + "/profile")
	if e != nil {
		t.Fatal(e)
	}
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(b), `lang="en"`) || !strings.Contains(string(b), "My profile") {
		t.Fatal("English preference did not apply")
	}
	viewer, viewerMe = loginForTest(t, base, "settings-viewer", "settings-viewer-password")
	if viewerMe.User.Language != "en" {
		t.Fatal("language must survive sign-in")
	}
	request(t, viewer, "PATCH", base+"/api/v1/profile", viewerMe.CSRF, map[string]any{"language": "de"}, 400, nil)
	r, e = viewer.PostForm(base+"/ui/language", url.Values{"csrf": {viewerMe.CSRF}, "language": {"uk"}})
	if e != nil {
		t.Fatal(e)
	}
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(b), `lang="uk"`) || !strings.Contains(string(b), "Мій профіль") {
		t.Fatal("language switch form failed")
	}
	request(t, admin, "GET", base+"/api/v1/profile", "", nil, 200, &me)
	if me.User.Language != "uk" {
		t.Fatal("language changed another user")
	}
}
