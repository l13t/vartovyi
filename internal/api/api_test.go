package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"vartovyi/internal/config"
)

func TestAnonymousAPIIsJSON(t *testing.T) {
	a, e := New(context.Background(), config.Config{PublicURL: "http://localhost"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/v1/cameras", "/api/v1/me", "/api/v1/events", "/media/camera/live/index.m3u8"} {
		r := httptest.NewRecorder()
		a.Handler().ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 401 || r.Header().Get("Location") != "" || !strings.HasPrefix(r.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("API returned browser response for %s: %d", path, r.Code)
		}
		var v map[string]any
		if json.Unmarshal(r.Body.Bytes(), &v) != nil {
			t.Fatal("invalid JSON")
		}
	}
}
func TestInvalidPlaybackWindow(t *testing.T) {
	for _, q := range []string{"", "?start=2026-01-01T00:00:00Z&end=2026-01-03T00:00:00Z", "?start=2026-01-02T00:00:00Z&end=2026-01-01T00:00:00Z"} {
		_, _, e := interval(httptest.NewRequest("GET", "/"+q, nil))
		if e == nil {
			t.Fatal("invalid interval accepted")
		}
	}
}
