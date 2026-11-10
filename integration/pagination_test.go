package integration

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"vartovyi/internal/auth"
	"vartovyi/internal/model"
	"vartovyi/internal/store"
)

func TestListPagination(t *testing.T) {
	if os.Getenv("VARTOVYI_INTEGRATION") != "1" {
		t.Skip("requires disposable services")
	}
	base := os.Getenv("VARTOVYI_TEST_URL")
	admin, _ := loginForTest(t, base, "admin", "admin")
	s, e := store.Open(t.Context(), "postgres://vartovyi:test-password@postgres:5432/vartovyi?sslmode=disable", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	// Reuse one hash for synthetic records; no real accounts or footage are changed.
	hash, e := auth.HashPassword("pagination-fixture-password")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 31; i++ {
		_, e = s.Pool.Exec(t.Context(), "INSERT INTO api_users(id,username,role,password_hash) VALUES($1,$2,'viewer',$3)", model.ID(), fmt.Sprintf("pager-user-%02d", i), hash)
		if e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 11; i++ {
		c := model.DefaultCamera()
		c.ID = fmt.Sprintf("pager-camera-%02d", i)
		c.Name = c.ID
		c.Source = "rtsp"
		c.URL = "rtsp://source:8554/synthetic"
		c.Enabled = false
		if e = s.SaveCamera(t.Context(), c); e != nil {
			t.Fatal(e)
		}
	}
	now := time.Now().UTC().Add(-time.Minute)
	for i := 0; i < 121; i++ {
		e = s.Event(t.Context(), model.Event{ID: model.ID(), Type: "motion", CameraID: "pager-camera-00", ShardID: "main", At: now.Add(-time.Duration(i) * time.Minute), End: now.Add(-time.Duration(i) * time.Minute)})
		if e != nil {
			t.Fatal(e)
		}
	}
	get := func(path string) string {
		t.Helper()
		r, err := admin.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode != 200 {
			t.Fatalf("pagination status %d at %s", r.StatusCode, path)
		}
		return string(b)
	}
	for _, size := range []int{10, 25, 50, 100} {
		users := get(fmt.Sprintf("/users?q=user%%3A%%20pager-user-*&page_size=%d", size))
		want := 31
		if size < want {
			want = size
		}
		if strings.Count(users, `class="user-select"`) != want {
			t.Fatalf("wrong user page size %d", size)
		}
		events := get(fmt.Sprintf("/events?camera_id=pager-camera-00&page_size=%d", size))
		if strings.Count(events, `class="event"`) != size {
			t.Fatalf("wrong event page size %d", size)
		}
		cameras := get(fmt.Sprintf("/?page_size=%d", size))
		var list struct {
			Items []model.Camera `json:"items"`
		}
		request(t, admin, "GET", base+"/api/v1/cameras", "", nil, 200, &list)
		want = len(list.Items)
		if size < want {
			want = size
		}
		if strings.Count(cameras, `class="camera"`) != want {
			t.Fatalf("wrong camera page size %d", size)
		}
		if !strings.Contains(users, fmt.Sprintf(`<option value="%d" selected`, size)) {
			t.Fatal("page size selection lost")
		}
	}
	users := get("/users?q=" + url.QueryEscape("user: pager-user-*") + "&page_size=10&page=2")
	if !strings.Contains(users, "pager-user-10") || strings.Contains(users, ">pager-user-00<") {
		t.Fatal("user search page two incorrect")
	}
	users = get("/users?q=" + url.QueryEscape("user: pager-user-*") + "&page_size=10&page=999")
	if strings.Count(users, `class="user-select"`) != 1 || !strings.Contains(users, "pager-user-30") {
		t.Fatal("out-of-range page must show last page")
	}
	events := get("/events?camera_id=pager-camera-00&page_size=100&page=2")
	if strings.Count(events, `class="event"`) != 21 || !strings.Contains(events, "snapshot") {
		t.Fatal("last event page incorrect")
	}
}
