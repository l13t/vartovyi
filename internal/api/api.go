package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"vartovyi/internal/auth"
	"vartovyi/internal/config"
	"vartovyi/internal/contract"
	"vartovyi/internal/model"
	"vartovyi/internal/onvif"
	run "vartovyi/internal/runtime"
	"vartovyi/internal/store"
	"vartovyi/internal/telemetry"
	"vartovyi/internal/web"
)

type contextKey int

const principalKey contextKey = 0

type API struct {
	Config  config.Config
	Store   *store.Store
	Web     *web.Web
	oidc    *oidcLogin
	oidcMu  sync.Mutex
	loginMu sync.Mutex
	loginAt map[string]time.Time
}

func New(ctx context.Context, c config.Config, s *store.Store) (*API, error) {
	origin, err := url.Parse(c.PublicURL)
	if err != nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("public_url must be an HTTP(S) origin")
	}
	w, e := web.New()
	if e != nil {
		return nil, e
	}
	a := &API{Config: c, Store: s, Web: w, loginAt: map[string]time.Time{}}
	return a, nil
}
func Principal(r *http.Request) model.Principal {
	p, _ := r.Context().Value(principalKey).(model.Principal)
	return p
}
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string) {
	JSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": strings.ReplaceAll(code, "_", " ")}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		fail(w, 400, "invalid_request")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(w, 400, "invalid_request")
		return false
	}
	return true
}
func (a *API) authorize(role model.Role, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, e := r.Cookie("vartovyi_session")
		if e != nil {
			a.unauthorized(w, r)
			return
		}
		p, e := a.Store.Session(r.Context(), cookie.Value)
		if e != nil {
			a.unauthorized(w, r)
			return
		}
		if role == model.Admin && p.User.Role != model.Admin || role == model.Manager && !p.User.Role.CanManage() {
			fail(w, 403, "forbidden")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			csrf := r.Header.Get("X-CSRF-Token")
			if csrf == "" {
				_ = r.ParseForm()
				csrf = r.FormValue("csrf")
			}
			if !auth.Equal(csrf, p.CSRF) || !a.sameOrigin(r) {
				fail(w, 403, "csrf_failed")
				return
			}
		}
		h(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	}
}
func (a *API) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return origin == strings.TrimSuffix(a.Config.PublicURL, "/")
}
func (a *API) unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/media/") {
		fail(w, 401, "unauthorized")
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	run.Health(m, func() error { return a.Store.Ping(context.Background()) })
	m.Handle("GET /static/", http.StripPrefix("/static/", a.Web.Static()))
	m.HandleFunc("GET /login", a.loginPage)
	m.HandleFunc("POST /login", a.login)
	m.HandleFunc("POST /logout", a.authorize(model.Viewer, a.logout))
	m.HandleFunc("GET /auth/oidc", a.oidcStart)
	m.HandleFunc("GET /auth/oidc/callback", a.oidcCallback)
	m.HandleFunc("GET /api/v1/openapi.json", a.authorize(model.Viewer, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(contract.OpenAPI)
	}))
	m.HandleFunc("GET /api/v1/cameras/{id}", a.authorize(model.Viewer, func(w http.ResponseWriter, r *http.Request) {
		c, e := a.Store.Camera(r.Context(), r.PathValue("id"), false)
		if e != nil {
			fail(w, 404, "camera_not_found")
			return
		}
		JSON(w, 200, c)
	}))
	m.HandleFunc("GET /api/v1/me", a.authorize(model.Viewer, func(w http.ResponseWriter, r *http.Request) {
		p := Principal(r)
		JSON(w, 200, map[string]any{"user": p.User, "csrf_token": p.CSRF})
	}))
	m.HandleFunc("GET /api/v1/cameras", a.authorize(model.Viewer, a.cameras))
	m.HandleFunc("POST /api/v1/cameras", a.authorize(model.Manager, a.saveCamera))
	m.HandleFunc("PUT /api/v1/cameras/{id}", a.authorize(model.Manager, a.saveCamera))
	m.HandleFunc("DELETE /api/v1/cameras/{id}", a.authorize(model.Admin, a.deleteCamera))
	m.HandleFunc("GET /api/v1/status", a.authorize(model.Viewer, a.status))
	m.HandleFunc("GET /api/v1/events", a.authorize(model.Viewer, a.events))
	m.HandleFunc("GET /api/v1/cameras/{id}/timeline", a.authorize(model.Viewer, a.timeline))
	m.HandleFunc("POST /api/v1/exports", a.authorize(model.Manager, a.createExport))
	m.HandleFunc("GET /api/v1/exports/{id}", a.authorize(model.Manager, a.export))
	m.HandleFunc("GET /api/v1/camera-groups", a.authorize(model.Viewer, a.cameraGroups))
	m.HandleFunc("POST /api/v1/camera-groups", a.authorize(model.Admin, a.saveGroup))
	m.HandleFunc("DELETE /api/v1/camera-groups/{id}", a.authorize(model.Admin, a.deleteGroup))
	m.HandleFunc("GET /api/v1/settings/recording", a.authorize(model.Admin, a.getRecording))
	m.HandleFunc("PUT /api/v1/settings/recording", a.authorize(model.Admin, a.putRecording))
	m.HandleFunc("POST /ui/settings/recording", a.authorize(model.Admin, a.recordingForm))
	m.HandleFunc("POST /ui/camera-groups", a.authorize(model.Admin, a.groupForm))
	m.HandleFunc("GET /api/v1/users", a.authorize(model.Admin, a.users))
	m.HandleFunc("POST /api/v1/users/bulk-delete", a.authorize(model.Admin, a.deleteUsers))
	m.HandleFunc("POST /api/v1/users", a.authorize(model.Admin, a.saveUser))
	m.HandleFunc("POST /api/v1/onvif/discover", a.authorize(model.Manager, a.discover))
	m.HandleFunc("POST /api/v1/onvif/profiles", a.authorize(model.Manager, a.profiles))
	m.HandleFunc("GET /api/v1/stream", a.authorize(model.Viewer, a.sse))
	m.HandleFunc("GET /media/{camera}/{kind}/{rest...}", a.authorize(model.Viewer, a.media))
	m.HandleFunc("GET /api/v1/exports/{id}/download", a.authorize(model.Manager, a.download))
	m.HandleFunc("GET /api/v1/events/{id}/snapshot", a.authorize(model.Viewer, a.snapshot))
	m.HandleFunc("GET /{$}", a.authorize(model.Viewer, a.dashboard))
	m.HandleFunc("GET /cameras/{id}", a.authorize(model.Viewer, a.cameraPage))
	m.HandleFunc("GET /cameras/{id}/motion", a.authorize(model.Manager, a.cameraMotionPage))
	m.HandleFunc("GET /cameras/{id}/settings", a.authorize(model.Manager, a.cameraSettingsPage))
	m.HandleFunc("GET /cameras/new", a.authorize(model.Manager, a.newCameraPage))
	m.HandleFunc("GET /tools/onvif", a.authorize(model.Manager, a.onvifPage))
	m.HandleFunc("GET /events", a.authorize(model.Viewer, a.eventsPage))
	m.HandleFunc("GET /users/new", a.authorize(model.Admin, a.userEditorPage))
	m.HandleFunc("GET /users/{id}", a.authorize(model.Admin, a.userEditorPage))
	m.HandleFunc("GET /users", a.authorize(model.Admin, a.usersPage))
	m.HandleFunc("GET /api/v1/settings/oidc", a.authorize(model.Admin, a.getOIDCSettings))
	m.HandleFunc("PUT /api/v1/settings/oidc", a.authorize(model.Admin, a.putOIDCSettings))
	m.HandleFunc("POST /ui/settings/oidc", a.authorize(model.Admin, a.oidcSettingsForm))
	m.HandleFunc("GET /api/v1/settings/telegram", a.authorize(model.Admin, a.getTelegram))
	m.HandleFunc("PUT /api/v1/settings/telegram", a.authorize(model.Admin, a.putTelegram))
	m.HandleFunc("POST /ui/settings/telegram", a.authorize(model.Admin, a.telegramForm))
	m.HandleFunc("GET /settings", a.authorize(model.Admin, a.settings))
	m.HandleFunc("POST /ui/language", a.authorize(model.Viewer, a.languageForm))
	m.HandleFunc("GET /profile", a.authorize(model.Viewer, a.profilePage))
	m.HandleFunc("POST /ui/profile/{section}", a.authorize(model.Viewer, a.profileForm))
	m.HandleFunc("GET /api/v1/profile", a.authorize(model.Viewer, a.getProfile))
	m.HandleFunc("PATCH /api/v1/profile", a.authorize(model.Viewer, a.patchProfile))
	m.HandleFunc("POST /ui/cameras", a.authorize(model.Manager, a.saveCameraForm))
	m.HandleFunc("POST /ui/zones/{id}", a.authorize(model.Manager, a.saveZones))
	m.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if !auth.Equal(r.Header.Get("Authorization"), "Bearer "+a.Config.ServiceToken) {
			http.Error(w, "unauthorized", 401)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		var pending, failed int64
		if a.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM event_outbox").Scan(&pending) != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		_ = a.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM notifier_deliveries WHERE state='retry' AND attempts>=10").Scan(&failed)
		fmt.Fprintf(w, "vartovyi_api_up 1\nvartovyi_outbox_pending %d\nvartovyi_notifications_failed %d\n", pending, failed)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		m.ServeHTTP(w, r)
	})
}
func (a *API) loginPage(w http.ResponseWriter, r *http.Request) {
	language := "uk"
	if cookie, e := r.Cookie("vartovyi_language"); e == nil && cookie.Value == "en" {
		language = "en"
	}
	if selected := r.URL.Query().Get("lang"); selected == "en" || selected == "uk" {
		language = selected
		a.languageCookie(w, language)
	}
	token := auth.Token()
	http.SetCookie(w, &http.Cookie{Name: "vartovyi_login", Value: token, Path: "/login", HttpOnly: true, Secure: a.Config.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: 600})
	settings, e := a.oidcSettings(r.Context())
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	a.Web.Render(w, "login", map[string]any{"CSRF": token, "OIDC": settings.Enabled, "Language": language})
}
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	_ = r.ParseForm()
	cookie, e := r.Cookie("vartovyi_login")
	if e != nil || !auth.Equal(cookie.Value, r.FormValue("csrf")) || !a.sameOrigin(r) {
		fail(w, 403, "csrf_failed")
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.loginMu.Lock()
	last := a.loginAt[host]
	if len(a.loginAt) > 4096 {
		for k, v := range a.loginAt {
			if time.Since(v) > time.Minute {
				delete(a.loginAt, k)
			}
		}
	}
	if len(a.loginAt) >= 4096 && last.IsZero() {
		a.loginMu.Unlock()
		fail(w, 429, "rate_limited")
		return
	}
	a.loginAt[host] = time.Now()
	a.loginMu.Unlock()
	if time.Since(last) < time.Second {
		fail(w, 429, "rate_limited")
		return
	}
	u, e := a.Store.UserByName(r.Context(), r.FormValue("username"))
	if e != nil || !auth.VerifyPassword(u.PasswordHash, r.FormValue("password")) {
		fail(w, 401, "invalid_credentials")
		return
	}
	a.startSession(w, r, u)
}
func (a *API) startSession(w http.ResponseWriter, r *http.Request, u model.User) {
	token, _, e := a.Store.NewSession(r.Context(), u)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "vartovyi_session", Value: token, Path: "/", HttpOnly: true, Secure: a.Config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 86400})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	_ = a.Store.Logout(r.Context(), Principal(r).SessionID)
	http.SetCookie(w, &http.Cookie{Name: "vartovyi_session", Value: "", Path: "/", MaxAge: -1, Secure: a.Config.CookieSecure, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (a *API) cameras(w http.ResponseWriter, r *http.Request) {
	cs, e := a.Store.Cameras(r.Context(), false)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	JSON(w, 200, map[string]any{"items": cs})
}
func (a *API) putCamera(ctx context.Context, p model.Principal, c model.Camera) (model.Camera, error) {
	if c.ID == "" {
		c.ID = model.ID()
	}
	if _, ok := a.Config.ShardURLs[c.ShardID]; !ok {
		return c, errors.New("unknown shard")
	}
	old, e := a.Store.Camera(ctx, c.ID, true)
	if e == nil {
		if c.MotionEnabled == nil {
			c.MotionEnabled = old.MotionEnabled
		}
		if old.ShardID != c.ShardID {
			return c, errors.New("shard reassignment requires offline archive migration")
		}
		if c.URL == "" {
			c.URL = old.URL
		}
		if c.Substream == "__unchanged__" {
			c.Substream = old.Substream
		}
		if p.User.Role != model.Admin && (c.RetentionDays != old.RetentionDays || c.QuotaBytes != old.QuotaBytes || c.MaxSegments != old.MaxSegments) {
			return c, errors.New("only admin may change retention or quota")
		}
	} else if !errors.Is(e, store.ErrNotFound) {
		return c, e
	} else if p.User.Role != model.Admin {
		c.RetentionDays = 14
		c.QuotaBytes = 0
		c.MaxSegments = 0
	}
	if e = c.Validate(); e != nil {
		return c, e
	}
	if e = onvif.Allowed(ctx, c.URL, a.Config.CameraCIDRs); e != nil {
		return c, e
	}
	if c.Substream != "" {
		if e = onvif.Allowed(ctx, c.Substream, a.Config.CameraCIDRs); e != nil {
			return c, e
		}
	}
	e = a.Store.SaveCamera(ctx, c)
	return c.Public(), e
}
func (a *API) saveCamera(w http.ResponseWriter, r *http.Request) {
	var c model.Camera
	if !decode(w, r, &c) {
		return
	}
	if id := r.PathValue("id"); id != "" {
		if c.ID != "" && c.ID != id {
			fail(w, 400, "id_mismatch")
			return
		}
		c.ID = id
	}
	saved, e := a.putCamera(r.Context(), Principal(r), c)
	if e != nil {
		if errors.Is(e, store.ErrConflict) {
			fail(w, 409, "revision_conflict")
		} else {
			fail(w, 400, "invalid_camera")
		}
		return
	}
	fresh, e := a.Store.Camera(r.Context(), saved.ID, false)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	JSON(w, 200, fresh)
}
func (a *API) deleteCamera(w http.ResponseWriter, r *http.Request) {
	if e := a.Store.DeleteCamera(r.Context(), r.PathValue("id")); e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	w.WriteHeader(204)
}
func (a *API) status(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.Statuses(r.Context())
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	JSON(w, 200, map[string]any{"items": v})
}
func (a *API) events(w http.ResponseWriter, r *http.Request) {
	before, beforeID, e := parseEventCursor(r.URL.Query().Get("cursor"))
	if e != nil {
		fail(w, 400, "invalid_cursor")
		return
	}
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 1 || n > 200 {
			fail(w, 400, "invalid_limit")
			return
		}
		limit = n
	}
	v, e := a.Store.EventsPage(r.Context(), r.URL.Query().Get("camera_id"), before, beforeID, limit)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	next := ""
	if len(v) == limit {
		next = base64.RawURLEncoding.EncodeToString([]byte(v[len(v)-1].At.Format(time.RFC3339Nano) + "|" + v[len(v)-1].ID))
	}
	JSON(w, 200, map[string]any{"items": v, "next_cursor": next})
}
func parseEventCursor(cursor string) (time.Time, string, error) {
	before := time.Now().Add(time.Second)
	if cursor == "" {
		return before, "~", nil
	}
	b, e := base64.RawURLEncoding.DecodeString(cursor)
	if e != nil {
		return before, "", e
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 || !model.ValidID(parts[1]) {
		return before, "", errors.New("invalid cursor")
	}
	before, e = time.Parse(time.RFC3339Nano, parts[0])
	return before, parts[1], e
}
func interval(r *http.Request) (time.Time, time.Time, error) {
	start, e := time.Parse(time.RFC3339Nano, r.URL.Query().Get("start"))
	if e != nil {
		return start, time.Time{}, e
	}
	end, e := time.Parse(time.RFC3339Nano, r.URL.Query().Get("end"))
	if e != nil || !end.After(start) || end.Sub(start) > 24*time.Hour {
		return start, end, errors.New("interval must be positive and at most 24 hours")
	}
	return start, end, nil
}
func (a *API) timeline(w http.ResponseWriter, r *http.Request) {
	start, end, e := interval(r)
	if e != nil {
		fail(w, 400, "invalid_interval")
		return
	}
	v, e := a.Store.Segments(r.Context(), r.PathValue("id"), start, end)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	JSON(w, 200, map[string]any{"items": v})
}
func (a *API) createExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CameraID string    `json:"camera_id"`
		Start    time.Time `json:"start"`
		End      time.Time `json:"end"`
	}
	if !decode(w, r, &req) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > 128 || !req.End.After(req.Start) || req.End.Sub(req.Start) > time.Hour {
		fail(w, 400, "invalid_export")
		return
	}
	c, e := a.Store.Camera(r.Context(), req.CameraID, false)
	if e != nil {
		fail(w, 404, "camera_not_found")
		return
	}
	v := model.Export{ID: model.ID(), UserID: Principal(r).User.ID, CameraID: c.ID, ShardID: c.ShardID, Start: req.Start, End: req.End, Status: "pending"}
	v, e = a.Store.CreateExport(r.Context(), v, key)
	if e != nil {
		fail(w, 409, "export_conflict")
		return
	}
	w.Header().Set("Location", "/api/v1/exports/"+v.ID)
	JSON(w, 202, v)
}
func (a *API) export(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.Export(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, 404, "export_not_found")
		return
	}
	JSON(w, 200, v)
}
func (a *API) users(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.Users(r.Context())
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	v, e = searchUsers(v, r.URL.Query().Get("q"))
	if e != nil {
		fail(w, 400, "invalid_user_search")
		return
	}
	JSON(w, 200, map[string]any{"items": v})
}
func (a *API) saveUser(w http.ResponseWriter, r *http.Request) {
	var v struct {
		ID       string     `json:"id"`
		Username string     `json:"username"`
		Role     model.Role `json:"role"`
		Password string     `json:"password"`
		Name     string     `json:"name"`
	}
	if !decode(w, r, &v) {
		return
	}
	u := model.User{ID: v.ID, Username: v.Username, Role: v.Role, Name: v.Name}
	if e := a.Store.SaveUser(r.Context(), u, v.Password); e != nil {
		fail(w, 400, "invalid_user")
		return
	}
	w.WriteHeader(204)
}
func (a *API) discover(w http.ResponseWriter, r *http.Request) {
	c, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	v, e := onvif.Discover(c, a.Config.DiscoveryInterface)
	if e != nil {
		fail(w, 503, "discovery_unavailable")
		return
	}
	JSON(w, 200, map[string]any{"items": v})
}
func (a *API) profiles(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Address  string `json:"address"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &v) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	profiles, e := onvif.Profiles(ctx, v.Address, v.Username, v.Password, a.Config.CameraCIDRs)
	if e != nil {
		fail(w, 400, "onvif_failed")
		return
	}
	JSON(w, 200, map[string]any{"items": profiles})
}
func (a *API) media(w http.ResponseWriter, r *http.Request) {
	c, e := a.Store.Camera(r.Context(), r.PathValue("camera"), false)
	if e != nil {
		fail(w, 404, "camera_not_found")
		return
	}
	kind := r.PathValue("kind")
	if kind != "live" && kind != "playback" {
		fail(w, 404, "not_found")
		return
	}
	if kind == "playback" {
		if _, _, e = interval(r); e != nil {
			fail(w, 400, "invalid_interval")
			return
		}
		q := r.URL.Query()
		start, _ := time.Parse(time.RFC3339Nano, q.Get("start"))
		end, _ := time.Parse(time.RFC3339Nano, q.Get("end"))
		q.Set("duration", fmt.Sprint(end.Sub(start).Seconds()))
		q.Del("end")
		q.Set("format", "mp4")
		r.URL.RawQuery = q.Encode()
	}
	a.proxy(w, r, c.ShardID, "/media/"+c.ID+"/"+kind+"/"+r.PathValue("rest"))
}
func (a *API) proxy(w http.ResponseWriter, r *http.Request, shard, path string) {
	raw, ok := a.Config.ShardURLs[shard]
	if !ok {
		fail(w, 503, "shard_unavailable")
		return
	}
	target, e := url.Parse(raw)
	if e != nil {
		fail(w, 503, "shard_unavailable")
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = telemetry.Transport{Propagate: true}
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = path
		req.Header.Del("Cookie")
		req.Header.Set("Authorization", "Bearer "+a.Config.ServiceToken)
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) { fail(w, 503, "media_unavailable") }
	proxy.ServeHTTP(w, r)
}
func (a *API) download(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.Export(r.Context(), r.PathValue("id"))
	if e != nil || v.Status != "ready" {
		fail(w, 404, "export_not_ready")
		return
	}
	a.proxy(w, r, v.ShardID, "/files/exports/"+v.ID)
}
func (a *API) snapshot(w http.ResponseWriter, r *http.Request) {
	var payload []byte
	if e := a.Store.Pool.QueryRow(r.Context(), "SELECT payload FROM recorder_events WHERE id=$1", r.PathValue("id")).Scan(&payload); e != nil {
		fail(w, 404, "event_not_found")
		return
	}
	var v model.Event
	if json.Unmarshal(payload, &v) != nil {
		fail(w, 503, "event_unavailable")
		return
	}
	a.proxy(w, r, v.ShardID, "/files/snapshots/"+v.ID)
}
func (a *API) sse(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "stream_unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		status, e := a.Store.Statuses(r.Context())
		if e != nil {
			return
		}
		b, _ := json.Marshal(map[string]any{"version": 1, "items": status})
		fmt.Fprintf(w, "id: %d\nevent: status\ndata: %s\n\n", time.Now().UnixNano(), b)
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		if cookie, e := r.Cookie("vartovyi_session"); e != nil {
			return
		} else if _, e = a.Store.Session(r.Context(), cookie.Value); e != nil {
			return
		}
	}
}
