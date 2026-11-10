package integration

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"vartovyi/internal/model"
	"vartovyi/internal/store"
)

func TestCameraGroupsAndRetention(t *testing.T) {
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
	var settings model.RecordingSettings
	request(t, admin, "GET", base+"/api/v1/settings/recording", "", nil, 200, &settings)
	request(t, admin, "POST", base+"/api/v1/camera-groups", me.CSRF, map[string]any{"name": "Двір", "revision": settings.Revision}, 200, &settings)
	group := settings.Groups[0]
	s, e := store.Open(t.Context(), "postgres://vartovyi:test-password@postgres:5432/vartovyi?sslmode=disable", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	cameras := []model.Camera{}
	segmentIDs := []string{}
	defer func() {
		for _, id := range segmentIDs {
			_ = s.DeleteSegment(t.Context(), id)
		}
		for _, c := range cameras {
			_ = s.DeleteCamera(t.Context(), c.ID)
		}
		current, err := s.Recording(t.Context())
		if err == nil {
			current.Groups = []model.CameraGroup{}
			current.Global = model.RecordingLimits{}
			_ = s.SaveRecording(t.Context(), current)
		}
	}()
	for _, id := range []string{"quota-a", "quota-b"} {
		c := model.DefaultCamera()
		c.ID = id
		c.Name = "Retention " + id
		c.Source = "rtsp"
		c.URL = "rtsp://source:8554/synthetic"
		c.Enabled = false
		c.GroupID = group.ID
		c.RetentionDays = 0
		request(t, admin, "POST", base+"/api/v1/cameras", me.CSRF, c, 200, &c)
		cameras = append(cameras, c)
	}
	// Quota values link to dedicated editors; group management has its own tab.
	overview, err := admin.Get(base + "/settings?tab=recording")
	if err != nil {
		t.Fatal(err)
	}
	overviewBody, _ := io.ReadAll(overview.Body)
	overview.Body.Close()
	decodedOverview, _ := url.QueryUnescape(string(overviewBody))
	if strings.Contains(string(overviewBody), `action="/ui/settings/recording"`) || !strings.Contains(decodedOverview, "scope=group:"+group.ID) || !strings.Contains(decodedOverview, "scope=camera:"+cameras[0].ID) {
		t.Fatal("quota overview must be clickable, without inline form")
	}
	overview, err = admin.Get(base + "/settings?tab=camera-groups")
	if err != nil {
		t.Fatal(err)
	}
	overviewBody, _ = io.ReadAll(overview.Body)
	overview.Body.Close()
	groupPage := string(overviewBody)
	start := strings.Index(groupPage, "data-default-group")
	if start < 0 {
		t.Fatal("default group missing")
	}
	end := strings.Index(groupPage[start:], "</section>")
	if end < 0 {
		t.Fatal("default group card incomplete")
	}
	defaultCard := groupPage[start : start+end]
	for _, c := range cameras {
		if !strings.Contains(defaultCard, c.Name) {
			t.Fatal("default must contain grouped cameras")
		}
	}
	if strings.Contains(defaultCard, "<form") {
		t.Fatal("default must not be editable")
	}
	request(t, admin, "DELETE", base+"/api/v1/camera-groups/"+group.ID, me.CSRF, map[string]any{"revision": settings.Revision}, 409, nil)
	r, e := admin.Get(base + "/?group=" + group.ID)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(body), "Retention quota-a") || !strings.Contains(string(body), "Retention quota-b") {
		t.Fatal("camera group filter failed")
	}
	for _, scope := range []string{"global", "group:" + group.ID, "camera:" + cameras[0].ID} {
		request(t, admin, "GET", base+"/api/v1/settings/recording", "", nil, 200, &settings)
		var c model.Camera
		request(t, admin, "GET", base+"/api/v1/cameras/"+cameras[0].ID, "", nil, 200, &c)
		form := url.Values{"csrf": {me.CSRF}, "scope": {scope}, "revision": {strconv.FormatInt(settings.Revision, 10)}, "camera_revision": {strconv.FormatInt(c.Revision, 10)}, "max_age_days": {"30"}, "max_segments": {"100000"}, "max_gib": {"1024"}}
		r, e = admin.PostForm(base+"/ui/settings/recording", form)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || strings.Contains(string(b), "recording_error=") {
			t.Fatal("recording scope form failed")
		}
	}
	request(t, admin, "GET", base+"/api/v1/settings/recording", "", nil, 200, &settings)
	if settings.Global.MaxAgeDays != 30 || settings.Global.MaxSegments != 100000 || settings.Groups[0].Limits.MaxBytes != 1024*(1<<30) {
		t.Fatal("global/group limits did not save")
	}
	var savedCamera model.Camera
	request(t, admin, "GET", base+"/api/v1/cameras/"+cameras[0].ID, "", nil, 200, &savedCamera)
	if savedCamera.MaxSegments != 100000 || savedCamera.RetentionDays != 30 {
		t.Fatal("camera limits did not save")
	}
	// Connection edits must preserve limits that now live in the Recording tab.
	cameraForm := url.Values{"csrf": {me.CSRF}, "id": {savedCamera.ID}, "revision": {strconv.FormatInt(savedCamera.Revision, 10)}, "name": {savedCamera.Name}, "source": {"rtsp"}, "shard_id": {"main"}, "fps": {"10"}, "mode": {"continuous"}}
	r, e = admin.PostForm(base+"/ui/cameras", cameraForm)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("camera edit failed")
	}
	request(t, admin, "GET", base+"/api/v1/cameras/"+savedCamera.ID, "", nil, 200, &savedCamera)
	if savedCamera.RetentionDays != 30 || savedCamera.MaxSegments != 100000 || savedCamera.QuotaBytes != 1024*(1<<30) || savedCamera.GroupID != group.ID {
		t.Fatal("connection edit reset recording policy or group")
	}
	// The planner below uses synthetic metadata, never files or persisted policies.
	now := time.Now().UTC()
	makeSegment := func(id, camera, shard string, age time.Duration, bytes int64) {
		segment := model.Segment{ID: id, CameraID: camera, ShardID: shard, Path: "segments/" + camera + "/" + id + ".mp4", Start: now.Add(-age - time.Minute), End: now.Add(-age), Bytes: bytes, Keep: true}
		if e = s.Segment(t.Context(), segment); e != nil {
			t.Fatal(e)
		}
		segmentIDs = append(segmentIDs, id)
	}
	makeSegment("qa-old", cameras[0].ID, "alpha", 72*time.Hour, 10)
	makeSegment("qa-mid", cameras[0].ID, "alpha", 8*time.Hour, 20)
	makeSegment("qa-new", cameras[0].ID, "alpha", 4*time.Hour, 30)
	makeSegment("qb-old", cameras[1].ID, "beta", 9*time.Hour, 15)
	makeSegment("qb-new", cameras[1].ID, "beta", 3*time.Hour, 25)
	makeSegment("qb-protected", cameras[1].ID, "beta", 30*time.Second, 5)
	policy := model.RecordingSettings{Groups: []model.CameraGroup{{ID: group.ID, Name: "Двір"}}}
	check := func(wantAlpha, wantBeta []string) {
		t.Helper()
		for _, tt := range []struct {
			shard string
			want  []string
		}{{"alpha", wantAlpha}, {"beta", wantBeta}} {
			got, err := s.RetentionCandidates(t.Context(), policy, cameras, tt.shard)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("%s retention plan: %v want %v", tt.shard, got, tt.want)
			}
			for _, id := range tt.want {
				if !got[id] {
					t.Fatalf("missing retention candidate %s", id)
				}
			}
		}
	}
	check(nil, nil)
	policy.Groups[0].Limits.MaxSegments = 3
	check([]string{"qa-old", "qa-mid"}, []string{"qb-old"})
	policy.Global.MaxSegments = 2
	check([]string{"qa-old", "qa-mid", "qa-new"}, []string{"qb-old"})
	policy.Groups[0].Limits = model.RecordingLimits{}
	policy.Global = model.RecordingLimits{MaxBytes: 35}
	check([]string{"qa-old", "qa-mid", "qa-new"}, []string{"qb-old"})
	policy.Global = model.RecordingLimits{MaxBytes: 4}
	check([]string{"qa-old", "qa-mid", "qa-new"}, []string{"qb-old", "qb-new"})
	policy.Global = model.RecordingLimits{MaxAgeDays: 1}
	check([]string{"qa-old"}, nil)
	policy.Global = model.RecordingLimits{MaxSegments: 4}
	cameras[0].MaxSegments = 1
	check([]string{"qa-old", "qa-mid"}, nil)
	// A restrictive camera policy is removed from totals before the global stage.
	cameras[0].MaxSegments = 0
	policy.Global = model.RecordingLimits{}
	policy.Groups[0].Limits.MaxAgeDays = 1
	check([]string{"qa-old"}, nil)
	// Permission gates and shared public group names.
	id := model.ID()
	request(t, admin, "POST", base+"/api/v1/users", me.CSRF, map[string]any{"id": id, "username": "group-manager", "password": "group-manager-password", "role": "manager"}, 204, nil)
	manager, managerMe := loginForTest(t, base, "group-manager", "group-manager-password")
	request(t, manager, "GET", base+"/api/v1/camera-groups", "", nil, 200, nil)
	request(t, manager, "GET", base+"/api/v1/settings/recording", "", nil, 403, nil)
	request(t, manager, "PUT", base+"/api/v1/settings/recording", managerMe.CSRF, policy, 403, nil)
	request(t, admin, "POST", base+"/api/v1/users/bulk-delete", me.CSRF, map[string]any{"ids": []string{id}}, 200, nil)
}
