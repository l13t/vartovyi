package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"vartovyi/internal/buildinfo"
	"vartovyi/internal/model"
)

func TestPagesEscapeCameraInput(t *testing.T) {
	w, e := New()
	if e != nil {
		t.Fatal(e)
	}
	c := model.DefaultCamera()
	c.Name = `<script>alert(1)</script>`
	data := map[string]any{"CSRF": "csrf", "User": model.User{Username: "admin", Role: model.Admin}, "Camera": c, "Cameras": []model.Camera{c}, "CanManage": true, "IsAdmin": true, "Zones": "[]", "Shards": map[string]string{"main": "http://internal"}}
	for _, page := range []string{"login", "dashboard", "camera", "settings"} {
		r := httptest.NewRecorder()
		w.Render(r, page, data)
		if r.Code != 200 || strings.Contains(r.Body.String(), c.Name) {
			t.Fatalf("unsafe or failed %s: %s", page, r.Body.String())
		}
		if !strings.Contains(r.Body.String(), "/static/app.js") {
			t.Fatal("missing embedded assets")
		}
		if !strings.Contains(r.Body.String(), buildinfo.Version) || !strings.Contains(r.Body.String(), Translate("uk", buildinfo.License)) {
			t.Fatal("missing build version or license in footer")
		}
	}
}

func TestManagementPagesAreSeparated(t *testing.T) {
	w, e := New()
	if e != nil {
		t.Fatal(e)
	}
	data := map[string]any{"User": model.User{Username: "admin", Role: model.Admin}, "CSRF": "token", "CanManage": true, "IsAdmin": true, "Nav": "settings", "Shards": map[string]string{"main": "hidden"}, "Camera": model.DefaultCamera(), "Cameras": []model.Camera{}, "Events": []model.Event{}, "UserCounts": map[string]int{}, "Users": []model.User{}, "CanChangePassword": true}
	for _, name := range []string{"new-camera", "camera-wizard", "onvif", "users", "events", "profile", "camera-settings", "settings"} {
		r := httptest.NewRecorder()
		w.Render(r, name, data)
		if r.Code != 200 || strings.Contains(r.Body.String(), "render failed") {
			t.Fatalf("render %s failed: %s", name, r.Body.String())
		}
		if name == "settings" && (strings.Contains(r.Body.String(), `id="user-form"`) || strings.Contains(r.Body.String(), `id="onvif-profiles"`) || strings.Contains(r.Body.String(), `data-camera-form`)) {
			t.Fatal("general settings still mixes unrelated tools")
		}
	}
	data["IsAdmin"] = false
	data["CanManage"] = false
	data["User"] = model.User{Username: "viewer", Role: model.Viewer}
	r := httptest.NewRecorder()
	w.Render(r, "profile", data)
	page := r.Body.String()
	if !strings.Contains(page, `action="/ui/profile/password"`) || strings.Contains(page, `href="/users"`) {
		t.Fatal("viewer profile must allow own password without admin navigation")
	}
}

func TestEnglishInterfacePreservesUserContent(t *testing.T) {
	w, e := New()
	if e != nil {
		t.Fatal(e)
	}
	camera := model.DefaultCamera()
	camera.Name = "Камери"
	data := map[string]any{"Users": []model.User{}, "UserCounts": map[string]int{}, "Shards": map[string]string{}, "Language": "en", "User": model.User{Username: "Твої камери", Role: model.Admin}, "CSRF": "token", "Camera": camera, "Cameras": []model.Camera{camera}, "Nav": "cameras", "IsAdmin": true, "CanManage": true}
	for _, page := range []string{"dashboard", "profile", "settings", "events", "camera-settings", "users", "new-camera", "camera-wizard", "onvif"} {
		r := httptest.NewRecorder()
		w.Render(r, page, data)
		body := r.Body.String()
		if r.Code != 200 || strings.Contains(body, "render failed") || !strings.Contains(body, `lang="en"`) || !strings.Contains(body, "General settings") {
			t.Fatalf("English rendering failed: %s", page)
		}
		if !strings.Contains(body, "Твої камери") {
			t.Fatal("user name was translated")
		}
		if page == "dashboard" && (!strings.Contains(body, "Your cameras") || !strings.Contains(body, ">Камери</h2>")) {
			t.Fatal("camera name must remain unchanged")
		}
	}
}

func TestSettingsTabs(t *testing.T) {
	w, e := New()
	if e != nil {
		t.Fatal(e)
	}
	for _, language := range []string{"uk", "en"} {
		for _, tab := range []string{"general", "recording", "camera-groups", "telegram", "oidc"} {
			data := map[string]any{"Language": language, "User": model.User{Role: model.Admin}, "CSRF": "token", "IsAdmin": true, "RecordingCameras": []model.Camera{}, "SettingsTab": tab, "Shards": map[string]string{}, "OIDC": map[string]any{"Revision": int64(0)}}
			r := httptest.NewRecorder()
			w.Render(r, "settings", data)
			body := r.Body.String()
			if r.Code != 200 || strings.Contains(body, "render failed") {
				t.Fatalf("tab render failed: %s %s", language, tab)
			}
			for _, other := range []string{"telegram", "oidc"} {
				hasForm := strings.Contains(body, `action="/ui/settings/`+other+`"`)
				if hasForm != (tab == other) {
					t.Fatalf("unrelated panel rendered: %s %s", tab, other)
				}
			}
			if !strings.Contains(body, `href="/settings?tab=`+tab+`" aria-current="page"`) {
				t.Fatal("active tab not marked")
			}
			if language == "en" && tab == "oidc" && !strings.Contains(body, "Group to role mapping") {
				t.Fatal("OIDC panel untranslated")
			}
		}
	}
}

func TestMotionPanelAndUserCreationAreSeparate(t *testing.T) {
	w, e := New()
	if e != nil {
		t.Fatal(e)
	}
	camera := model.DefaultCamera()
	camera.Zones = []model.Zone{{Name: "Двір <script>", Points: []model.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}}}}
	for _, language := range []string{"uk", "en"} {
		data := map[string]any{"Language": language, "User": model.User{ID: "admin", Role: model.Admin}, "CSRF": "token", "CanManage": true, "IsAdmin": true, "Camera": camera, "Zones": "[]", "Account": model.User{Role: model.Viewer}}
		for _, page := range []string{"camera-settings", "camera-motion", "user-editor"} {
			data["CameraTab"] = page
			r := httptest.NewRecorder()
			w.Render(r, page, data)
			body := r.Body.String()
			if r.Code != 200 || strings.Contains(body, "render failed") || strings.Contains(body, "Двір <script>") {
				t.Fatal("unsafe or failed render")
			}
			if strings.Contains(body, `id="zones-editor"`) != (page == "camera-motion") {
				t.Fatal("motion settings mixed with connection form")
			}
			if page == "camera-motion" && (!strings.Contains(body, "Двір &lt;script&gt;") || !strings.Contains(body, `id="zone-list"`)) {
				t.Fatal("saved zones missing")
			}
			if page == "user-editor" && (!strings.Contains(body, `id="generate-password"`) || !strings.Contains(body, `id="user-created" hidden`)) {
				t.Fatal("password generation/one-time display missing")
			}
		}
	}
}

func TestUserSearchAndRecordingControls(t *testing.T) {
	w, e := New()
	if e != nil {
		t.Fatal(e)
	}
	for _, language := range []string{"uk", "en"} {
		data := map[string]any{"Language": language, "User": model.User{ID: "admin", Role: model.Admin}, "CSRF": "token", "IsAdmin": true, "Users": []model.User{}, "UserQuery": "team: admin user: admin*", "RecordingEditing": true, "RecordingScope": "global", "Recording": model.RecordingSettings{Groups: []model.CameraGroup{}}, "RecordingScopes": []map[string]string{{"Value": "global", "Name": "All cameras"}}, "RecordingLimits": model.RecordingLimits{}, "RecordingGiB": "0", "SettingsTab": "recording"}
		r := httptest.NewRecorder()
		w.Render(r, "users", data)
		body := r.Body.String()
		if r.Code != 200 || strings.Contains(body, "render failed") || !strings.Contains(body, `name="q" value="team: admin user: admin*"`) || strings.Contains(body, "/users?role=") {
			t.Fatal("user search not rendered")
		}
		r = httptest.NewRecorder()
		w.Render(r, "settings", data)
		body = r.Body.String()
		if r.Code != 200 || strings.Contains(body, "render failed") || !strings.Contains(body, `name="max_segments"`) || !strings.Contains(body, `name="max_gib"`) {
			t.Fatal("recording scope controls missing")
		}
	}
}

func TestQuotaOverviewEditorAndDefaultGroup(t *testing.T) {
	w, e := New()
	if e != nil {
		t.Fatal(e)
	}
	cameras := []model.Camera{{ID: "yard", Name: "Двір", GroupID: "outside"}, {ID: "hall", Name: "Коридор"}}
	for _, language := range []string{"uk", "en"} {
		data := map[string]any{"Language": language, "User": model.User{Role: model.Admin}, "CSRF": "token", "IsAdmin": true, "SettingsTab": "recording", "Recording": model.RecordingSettings{}, "RecordingScopes": []map[string]any{{"Value": "global", "Name": "default", "Limits": model.RecordingLimits{}}}, "RecordingCameras": cameras}
		r := httptest.NewRecorder()
		w.Render(r, "settings", data)
		body := r.Body.String()
		if r.Code != 200 || strings.Contains(body, "render failed") || !strings.Contains(body, `scope=global`) || strings.Contains(body, `action="/ui/settings/recording"`) || strings.Contains(body, `action="/ui/camera-groups"`) {
			t.Fatal("quota overview must link to isolated editors")
		}
		data["SettingsTab"] = "camera-groups"
		r = httptest.NewRecorder()
		w.Render(r, "settings", data)
		body = r.Body.String()
		if r.Code != 200 || !strings.Contains(body, "Двір") || !strings.Contains(body, "Коридор") || !strings.Contains(body, "data-default-group") || strings.Contains(body, `action="/ui/settings/recording"`) {
			t.Fatal("default must include grouped and ungrouped cameras on separate management page")
		}
		start := strings.Index(body, "data-default-group")
		end := strings.Index(body[start:], "</section>")
		if end < 0 || strings.Contains(body[start:start+end], "<form") {
			t.Fatal("default group must have no modification controls")
		}
	}
}

func TestStoredStreamNoticeDoesNotRevealCredentials(t *testing.T) {
	w, e := New()
	if e != nil {
		t.Fatal(e)
	}
	c := model.DefaultCamera()
	c.Source = "rtsp"
	c.Transcode = true
	c.URL = "rtsp://user:private-password@camera/stream"
	c.Revision = 1
	data := map[string]any{"Language": "en", "User": model.User{Role: model.Admin}, "IsAdmin": true, "CanManage": true, "CSRF": "token", "Camera": c.Public(), "StreamConfigured": true, "SubstreamConfigured": true, "CameraTab": "camera-settings"}
	r := httptest.NewRecorder()
	w.Render(r, "camera-settings", data)
	body := r.Body.String()
	if r.Code != 200 || strings.Contains(body, "private-password") || !strings.Contains(body, "Stream address saved") || !strings.Contains(body, `name="transcode" type="checkbox" checked`) {
		t.Fatal("saved-address feedback or secret redaction failed")
	}
}
