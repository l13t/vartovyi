package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"vartovyi/internal/model"
	"vartovyi/internal/store"
	"vartovyi/internal/web"
)

func recordingError(e error) string {
	switch {
	case errors.Is(e, store.ErrConflict):
		return "revision_conflict"
	case errors.Is(e, store.ErrGroupInUse):
		return "camera_group_in_use"
	default:
		return "invalid_recording_settings"
	}
}
func (a *API) getRecording(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.Recording(r.Context())
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	JSON(w, 200, v)
}
func (a *API) putRecording(w http.ResponseWriter, r *http.Request) {
	var v model.RecordingSettings
	if !decode(w, r, &v) {
		return
	}
	if e := a.Store.SaveRecording(r.Context(), v); e != nil {
		status := 400
		if errors.Is(e, store.ErrConflict) || errors.Is(e, store.ErrGroupInUse) {
			status = 409
		}
		fail(w, status, recordingError(e))
		return
	}
	v.Revision++
	JSON(w, 200, v)
}
func (a *API) cameraGroups(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.Recording(r.Context())
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	type group struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	items := []group{}
	for _, g := range v.Groups {
		items = append(items, group{g.ID, g.Name})
	}
	JSON(w, 200, map[string]any{"items": items})
}
func (a *API) changeGroup(ctx context.Context, action, id, name string, revision int64) (model.RecordingSettings, error) {
	v, e := a.Store.Recording(ctx)
	if e != nil {
		return v, e
	}
	if v.Revision != revision {
		return v, store.ErrConflict
	}
	found := false
	switch action {
	case "create":
		v.Groups = append(v.Groups, model.CameraGroup{ID: model.ID(), Name: strings.TrimSpace(name)})
	case "rename", "delete":
		for i, g := range v.Groups {
			if g.ID == id {
				found = true
				if action == "rename" {
					v.Groups[i].Name = strings.TrimSpace(name)
				} else {
					v.Groups = append(v.Groups[:i], v.Groups[i+1:]...)
				}
				break
			}
		}
		if !found {
			return v, store.ErrNotFound
		}
	default:
		return v, errors.New("invalid group action")
	}
	e = a.Store.SaveRecording(ctx, v)
	if e == nil {
		v.Revision++
	}
	return v, e
}
func (a *API) groupForm(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fail(w, 400, "invalid_form")
		return
	}
	rev, e := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if e != nil {
		fail(w, 400, "invalid_form")
		return
	}
	_, e = a.changeGroup(r.Context(), r.FormValue("action"), r.FormValue("id"), r.FormValue("name"), rev)
	target := "/settings?tab=camera-groups"
	if e != nil {
		target += "&recording_error=" + url.QueryEscape(recordingError(e))
	} else {
		target += "&recording=saved"
	}
	http.Redirect(w, r, target, 303)
}
func (a *API) saveGroup(w http.ResponseWriter, r *http.Request) {
	var v struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Revision int64  `json:"revision"`
	}
	if !decode(w, r, &v) {
		return
	}
	action := "rename"
	if v.ID == "" {
		action = "create"
	}
	saved, e := a.changeGroup(r.Context(), action, v.ID, v.Name, v.Revision)
	if e != nil {
		fail(w, 400, recordingError(e))
		return
	}
	JSON(w, 200, saved)
}
func (a *API) deleteGroup(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &v) {
		return
	}
	saved, e := a.changeGroup(r.Context(), "delete", r.PathValue("id"), "", v.Revision)
	if e != nil {
		fail(w, 409, recordingError(e))
		return
	}
	JSON(w, 200, saved)
}
func (a *API) recordingForm(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fail(w, 400, "invalid_form")
		return
	}
	scope := r.FormValue("scope")
	target := "/settings?tab=recording&scope=" + url.QueryEscape(scope)
	v, e := a.Store.Recording(r.Context())
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	rev, e := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if e == nil && rev != v.Revision {
		e = store.ErrConflict
	}
	days, e1 := strconv.Atoi(r.FormValue("max_age_days"))
	count, e2 := strconv.ParseInt(r.FormValue("max_segments"), 10, 64)
	gib, e3 := strconv.ParseFloat(r.FormValue("max_gib"), 64)
	if e1 != nil || e2 != nil || e3 != nil || math.IsNaN(gib) || math.IsInf(gib, 0) || gib < 0 || gib > 1<<20 {
		e = errors.New("invalid limits")
	}
	limits := model.RecordingLimits{MaxAgeDays: days, MaxSegments: count}
	if e == nil {
		limits.MaxBytes = int64(math.Round(gib * (1 << 30)))
		e = limits.Validate()
	}
	if e == nil {
		switch {
		case scope == "global":
			v.Global = limits
			e = a.Store.SaveRecording(r.Context(), v)
		case strings.HasPrefix(scope, "group:"):
			found := false
			for i, g := range v.Groups {
				if g.ID == strings.TrimPrefix(scope, "group:") {
					v.Groups[i].Limits = limits
					found = true
					break
				}
			}
			if !found {
				e = store.ErrNotFound
			} else {
				e = a.Store.SaveRecording(r.Context(), v)
			}
		case strings.HasPrefix(scope, "camera:"):
			var c model.Camera
			c, e = a.Store.Camera(r.Context(), strings.TrimPrefix(scope, "camera:"), true)
			if e == nil {
				var cameraRev int64
				cameraRev, e = strconv.ParseInt(r.FormValue("camera_revision"), 10, 64)
				if e == nil && cameraRev != c.Revision {
					e = store.ErrConflict
				}
				if e == nil {
					c.RetentionDays = limits.MaxAgeDays
					c.MaxSegments = limits.MaxSegments
					c.QuotaBytes = limits.MaxBytes
					e = a.Store.SaveCamera(r.Context(), c)
				}
			}
		default:
			e = errors.New("unknown scope")
		}
	}
	if e != nil {
		target += "&recording_error=" + url.QueryEscape(recordingError(e))
	} else {
		target += "&recording=saved"
	}
	http.Redirect(w, r, target, 303)
}

type recordingScope struct {
	Value, Name, GiB string
	Limits           model.RecordingLimits
}
type recordingGroupView struct {
	model.CameraGroup
	CameraCount int
	Cameras     []model.Camera
}

func (a *API) recordingPageData(data map[string]any, r *http.Request, cameras []model.Camera) error {
	v, e := a.Store.Recording(r.Context())
	if e != nil {
		return e
	}
	language := Principal(r).User.Language
	tr := func(s string) string { return web.Translate(language, s) }
	makeScope := func(value, name string, limits model.RecordingLimits) recordingScope {
		size := strings.TrimSuffix(strings.TrimRight(strconv.FormatFloat(float64(limits.MaxBytes)/(1<<30), 'f', 3, 64), "0"), ".")
		if limits.MaxBytes > 0 && float64(limits.MaxBytes)/(1<<30) < .001 {
			size = "< 0.001"
		}
		return recordingScope{Value: value, Name: name, Limits: limits, GiB: size}
	}
	scopes := []recordingScope{makeScope("global", "default · "+tr("Усі камери"), v.Global)}
	groupRows := []recordingGroupView{}
	scope := r.URL.Query().Get("scope")
	limits := v.Global
	valid := scope == "global"
	cameraRevision := int64(0)
	for _, g := range v.Groups {
		scopes = append(scopes, makeScope("group:"+g.ID, tr("Група")+" · "+g.Name, g.Limits))
		members := []model.Camera{}
		for _, c := range cameras {
			if c.GroupID == g.ID {
				members = append(members, c)
			}
		}
		groupRows = append(groupRows, recordingGroupView{CameraGroup: g, CameraCount: len(members), Cameras: members})
		if scope == "group:"+g.ID {
			valid = true
			limits = g.Limits
		}
	}
	for _, c := range cameras {
		scopes = append(scopes, makeScope("camera:"+c.ID, tr("Камера")+" · "+c.Name, model.RecordingLimits{MaxAgeDays: c.RetentionDays, MaxSegments: c.MaxSegments, MaxBytes: c.QuotaBytes}))
		if scope == "camera:"+c.ID {
			valid = true
			limits = model.RecordingLimits{MaxAgeDays: c.RetentionDays, MaxSegments: c.MaxSegments, MaxBytes: c.QuotaBytes}
			cameraRevision = c.Revision
		}
	}
	if !valid {
		scope = "global"
	}
	data["RecordingEditing"] = valid && r.URL.Query().Get("scope") != ""
	data["RecordingScopeMissing"] = !valid && r.URL.Query().Get("scope") != ""
	for _, option := range scopes {
		if option.Value == scope {
			data["RecordingScopeName"] = option.Name
			break
		}
	}
	data["RecordingCameras"] = cameras
	data["Recording"] = v
	data["RecordingGroups"] = groupRows
	data["RecordingScopes"] = scopes
	data["RecordingScope"] = scope
	data["RecordingLimits"] = limits
	data["CameraRevision"] = cameraRevision
	data["RecordingGiB"] = strconv.FormatFloat(float64(limits.MaxBytes)/(1<<30), 'f', -1, 64)
	data["RecordingSaved"] = r.URL.Query().Get("recording") == "saved"
	switch code := r.URL.Query().Get("recording_error"); code {
	case "revision_conflict", "camera_group_in_use", "invalid_recording_settings":
		data["RecordingError"] = tr(code)
	}
	return nil
}
