package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"vartovyi/internal/model"
)

func (a *API) pageData(r *http.Request) map[string]any {
	p := Principal(r)
	nav := "cameras"
	switch {
	case strings.HasPrefix(r.URL.Path, "/events"):
		nav = "events"
	case strings.HasPrefix(r.URL.Path, "/users"):
		nav = "users"
	case strings.HasPrefix(r.URL.Path, "/settings"):
		nav = "settings"
	case strings.HasPrefix(r.URL.Path, "/profile"):
		nav = "profile"
	}
	return map[string]any{"User": p.User, "CSRF": p.CSRF, "CanManage": p.User.Role.CanManage(), "IsAdmin": p.User.Role == model.Admin, "Nav": nav, "Language": p.User.Language}
}
func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	data := a.pageData(r)
	cameras, e := a.Store.Cameras(r.Context(), false)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	groups, e := a.Store.Recording(r.Context())
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	names := map[string]string{}
	for _, g := range groups.Groups {
		names[g.ID] = g.Name
	}
	filter := r.URL.Query().Get("group")
	filtered := []model.Camera{}
	for _, c := range cameras {
		if filter == "" || (filter == "-" && c.GroupID == "") || c.GroupID == filter {
			filtered = append(filtered, c)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := strings.ToLower(names[filtered[i].GroupID]), strings.ToLower(names[filtered[j].GroupID])
		if a == b {
			return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name)
		}
		return a < b
	})
	data["Groups"] = groups.Groups
	data["GroupNames"] = names
	data["GroupFilter"] = filter
	cameras = filtered
	pager := paginate(r, len(cameras))
	data["Pagination"] = pager
	data["Cameras"] = cameras[pager.Offset:pager.To]
	a.Web.Render(w, "dashboard", data)
}
func (a *API) cameraPage(w http.ResponseWriter, r *http.Request) {
	a.renderCameraPage(w, r, "camera")
}
func (a *API) cameraSettingsPage(w http.ResponseWriter, r *http.Request) {
	a.renderCameraPage(w, r, "camera-settings")
}
func (a *API) cameraMotionPage(w http.ResponseWriter, r *http.Request) {
	a.renderCameraPage(w, r, "camera-motion")
}
func (a *API) renderCameraPage(w http.ResponseWriter, r *http.Request, page string) {
	c, e := a.Store.Camera(r.Context(), r.PathValue("id"), page == "camera-settings")
	if e != nil {
		fail(w, 404, "camera_not_found")
		return
	}
	data := a.pageData(r)
	data["StreamConfigured"] = c.URL != ""
	data["SubstreamConfigured"] = c.Substream != ""
	data["Camera"] = c.Public()
	b, _ := json.Marshal(c.Zones)
	data["Zones"] = string(b)
	groups, e := a.Store.Recording(r.Context())
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	data["Groups"] = groups.Groups
	data["CameraSettings"] = page == "camera-settings"
	data["CameraTab"] = page
	a.Web.Render(w, page, data)
}
func (a *API) settings(w http.ResponseWriter, r *http.Request) {
	data, e := a.settingsData(r)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	a.Web.Render(w, "settings", data)
}
func (a *API) settingsData(r *http.Request) (map[string]any, error) {
	data := a.pageData(r)
	cs, e := a.Store.Cameras(r.Context(), false)
	if e != nil {
		return nil, e
	}
	telegram, e := a.Store.Telegram(r.Context(), a.Config.TelegramToken, a.Config.TelegramChat)
	if e != nil {
		return nil, e
	}
	oidc, e := a.oidcSettings(r.Context())
	if e != nil {
		return nil, e
	}
	tab := r.URL.Query().Get("tab")
	switch tab {
	case "recording", "camera-groups", "telegram", "oidc":
	default:
		tab = "general"
	}
	if e = a.recordingPageData(data, r, cs); e != nil {
		return nil, e
	}
	data["SettingsTab"] = tab
	data["Telegram"] = telegram
	data["TelegramResult"] = r.URL.Query().Get("telegram")
	data["OIDC"] = oidc
	data["OIDCResult"] = r.URL.Query().Get("oidc")
	data["OIDCAdminGroups"] = oidcGroups(oidc.RoleMap, "admin")
	data["OIDCManagerGroups"] = oidcGroups(oidc.RoleMap, "manager")
	data["OIDCCallback"] = strings.TrimSuffix(a.Config.PublicURL, "/") + "/auth/oidc/callback"
	data["CameraCount"] = len(cs)
	data["PublicURL"] = a.Config.PublicURL
	data["OIDCEnabled"] = oidc.Enabled
	data["Shards"] = a.Config.ShardURLs
	return data, nil
}
func (a *API) newCameraPage(w http.ResponseWriter, r *http.Request) {
	data := a.pageData(r)
	groups, e := a.Store.Recording(r.Context())
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	data["Groups"] = groups.Groups
	data["Camera"] = model.DefaultCamera()
	data["Shards"] = a.Config.ShardURLs
	if r.URL.Query().Get("mode") == "manual" {
		a.Web.Render(w, "new-camera", data)
		return
	}
	a.Web.Render(w, "camera-wizard", data)
}
func (a *API) onvifPage(w http.ResponseWriter, r *http.Request) {
	a.Web.Render(w, "onvif", a.pageData(r))
}
func (a *API) usersPage(w http.ResponseWriter, r *http.Request) {
	data := a.pageData(r)
	query := r.URL.Query().Get("q")
	if query == "" {
		if legacy := model.Role(r.URL.Query().Get("role")); legacy.Valid() {
			query = "team: " + string(legacy)
		}
	}
	terms, searchErr := parseUserSearch(query)
	selected := []model.User{}
	requested, size := pageRequest(r)
	tail := make([]model.User, size)
	total := 0
	if searchErr == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		e := a.Store.WalkUsers(ctx, func(u model.User) {
			for _, term := range terms {
				if !term.matches(u) {
					return
				}
			}
			total++
			tail[(total-1)%size] = u
			if (total-1)/size+1 == requested {
				selected = append(selected, u)
			}
		})
		if e != nil {
			fail(w, 503, "database_unavailable")
			return
		}
	}
	pager := paginate(r, total)
	if pager.Page != requested {
		for i := pager.Offset; i < total; i++ {
			selected = append(selected, tail[i%size])
		}
	}
	data["Users"] = selected
	data["UserQuery"] = query
	data["Pagination"] = pager
	data["UserTotal"] = total
	if searchErr != nil {
		data["SearchError"] = true
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(400)
	}
	a.Web.Render(w, "users", data)
}
func (a *API) eventsPage(w http.ResponseWriter, r *http.Request) {
	before, beforeID, e := parseEventCursor(r.URL.Query().Get("cursor"))
	if e != nil {
		fail(w, 400, "invalid_cursor")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	cameraID := r.URL.Query().Get("camera_id")
	cs, e := a.Store.Cameras(ctx, false)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	if r.URL.Query().Get("cursor") == "" {
		before = time.Now().UTC()
		if snapshot := r.URL.Query().Get("snapshot"); snapshot != "" {
			before, e = time.Parse(time.RFC3339Nano, snapshot)
			if e != nil {
				fail(w, 400, "invalid_pagination")
				return
			}
		}
	}
	total, e := a.Store.EventsCount(ctx, cameraID, before, beforeID)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	pagerRequest := r.Clone(r.Context())
	pageURL := *r.URL
	pagerRequest.URL = &pageURL
	query := pageURL.Query()
	if query.Get("cursor") == "" {
		query.Set("snapshot", before.Format(time.RFC3339Nano))
	}
	pageURL.RawQuery = query.Encode()
	pager := paginate(pagerRequest, total)
	events, e := a.Store.EventsOffset(ctx, cameraID, before, beforeID, pager.Size, pager.Offset)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	names := map[string]string{}
	for _, c := range cs {
		names[c.ID] = c.Name
	}
	type eventView struct {
		model.Event
		CameraName string
	}
	views := []eventView{}
	for _, event := range events {
		name := names[event.CameraID]
		if name == "" {
			name = event.CameraID
		}
		views = append(views, eventView{event, name})
	}
	data := a.pageData(r)
	data["Events"] = views
	data["Cameras"] = cs
	data["FilterCamera"] = cameraID
	data["Pagination"] = pager
	a.Web.Render(w, "events", data)
}
func (a *API) saveCameraForm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if r.ParseForm() != nil {
		fail(w, 400, "invalid_form")
		return
	}
	c := model.DefaultCamera()
	c.ID = r.FormValue("id")
	c.Name = r.FormValue("name")
	c.ShardID = r.FormValue("shard_id")
	c.Source = r.FormValue("source")
	c.Transcode = r.FormValue("transcode") == "on"
	c.RTSPTransport = r.FormValue("rtsp_transport")
	c.URL = r.FormValue("url")
	c.Substream = r.FormValue("substream")
	c.Mode = r.FormValue("mode")
	c.Enabled = r.FormValue("enabled") == "on"
	c.Notify = r.FormValue("notify") == "on"
	c.FPS, _ = strconv.Atoi(r.FormValue("fps"))
	if r.Form.Has("retention_days") {
		c.RetentionDays, _ = strconv.Atoi(r.FormValue("retention_days"))
	}
	c.GroupID = r.FormValue("group_id")
	if r.Form.Has("max_segments") {
		c.MaxSegments, _ = strconv.ParseInt(r.FormValue("max_segments"), 10, 64)
	}
	if r.Form.Has("quota_bytes") {
		c.QuotaBytes, _ = strconv.ParseInt(r.FormValue("quota_bytes"), 10, 64)
	}
	c.Revision, _ = strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if r.FormValue("clear_substream") == "on" {
		c.Substream = ""
	} else if c.Revision > 0 && c.Substream == "" {
		c.Substream = "__unchanged__"
	}
	if old, e := a.Store.Camera(r.Context(), c.ID, true); e == nil {
		if !r.Form.Has("group_id") {
			c.GroupID = old.GroupID
		}
		if !r.Form.Has("connection_options") {
			c.Transcode = old.Transcode
			c.RTSPTransport = old.RTSPTransport
		}
		c.Zones = old.Zones
		c.PreSeconds = old.PreSeconds
		c.PostSeconds = old.PostSeconds
		c.Threshold = old.Threshold
		c.MotionEnabled = old.MotionEnabled
		c.MotionFullFrame = old.MotionFullFrame
		if !r.Form.Has("retention_days") {
			c.RetentionDays = old.RetentionDays
		}
		if !r.Form.Has("quota_bytes") {
			c.QuotaBytes = old.QuotaBytes
		}
		if !r.Form.Has("max_segments") {
			c.MaxSegments = old.MaxSegments
		}
		if Principal(r).User.Role != model.Admin {
			c.RetentionDays = old.RetentionDays
			c.QuotaBytes = old.QuotaBytes
			c.MaxSegments = old.MaxSegments
		}
	}
	if c.Source != "rtsp" {
		c.Transcode = false
		c.RTSPTransport = ""
	}
	saved, e := a.putCamera(r.Context(), Principal(r), c)
	if e != nil {
		fail(w, 400, "invalid_camera")
		return
	}
	target := "/cameras/" + saved.ID
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(204)
		return
	}
	http.Redirect(w, r, target, 303)
}
func (a *API) saveZones(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Revision        int64        `json:"revision"`
		Zones           []model.Zone `json:"zones"`
		Threshold       float64      `json:"threshold"`
		PreSeconds      *int         `json:"pre_seconds,omitempty"`
		PostSeconds     *int         `json:"post_seconds,omitempty"`
		MotionEnabled   *bool        `json:"motion_enabled,omitempty"`
		MotionFullFrame *bool        `json:"motion_full_frame,omitempty"`
	}
	if !decode(w, r, &v) {
		return
	}
	c, e := a.Store.Camera(r.Context(), r.PathValue("id"), true)
	if e != nil {
		fail(w, 404, "camera_not_found")
		return
	}
	if c.Revision != v.Revision {
		fail(w, 409, "revision_conflict")
		return
	}
	c.Zones = v.Zones
	c.Threshold = v.Threshold
	if v.MotionEnabled != nil {
		c.MotionEnabled = v.MotionEnabled
	}
	if v.MotionFullFrame != nil {
		c.MotionFullFrame = *v.MotionFullFrame
	}
	if v.PreSeconds != nil {
		c.PreSeconds = *v.PreSeconds
	}
	if v.PostSeconds != nil {
		c.PostSeconds = *v.PostSeconds
	}
	if _, e = a.putCamera(r.Context(), Principal(r), c); e != nil {
		fail(w, 400, "invalid_zones")
		return
	}
	JSON(w, 200, map[string]bool{"saved": true})
}
