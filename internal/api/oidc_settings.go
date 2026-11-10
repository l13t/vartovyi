package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"vartovyi/internal/store"
	"vartovyi/internal/web"
)

var errOIDCDiscovery = errors.New("OIDC discovery failed")

func (a *API) saveOIDCSettings(ctx context.Context, changes store.OIDCChanges) (store.OIDCSettings, error) {
	old, e := a.oidcSettings(ctx)
	if e != nil {
		return old, e
	}
	if old.Revision != changes.Revision {
		return old, store.ErrConflict
	}
	settings, e := store.PrepareOIDC(changes, old)
	if e != nil {
		return settings, e
	}
	var login *oidcLogin
	if settings.Enabled {
		login, e = newOIDC(ctx, a.oidcConfig(settings), a.Store)
		if e != nil {
			return settings, errOIDCDiscovery
		}
	}
	saved, e := a.Store.SaveOIDC(ctx, settings)
	if e != nil {
		return settings, e
	}
	a.oidcMu.Lock()
	a.oidc = login
	a.oidcMu.Unlock()
	return saved, nil
}
func oidcSettingsError(e error) (int, string, string) {
	switch {
	case errors.Is(e, store.ErrConflict):
		return 409, "revision_conflict", "Налаштування OIDC змінились. Онови сторінку перед збереженням."
	case errors.Is(e, store.ErrInvalidOIDC):
		return 400, "invalid_oidc_settings", "Перевір адресу провайдера, Client ID та відповідність груп ролям."
	case errors.Is(e, errOIDCDiscovery):
		return 502, "oidc_discovery_failed", "Не вдалося отримати конфігурацію провайдера OIDC. Попередні налаштування залишились активними."
	default:
		return 503, "database_unavailable", "Не вдалося зберегти зміни. Спробуй ще раз."
	}
}
func (a *API) getOIDCSettings(w http.ResponseWriter, r *http.Request) {
	v, e := a.oidcSettings(r.Context())
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	JSON(w, 200, v)
}
func (a *API) putOIDCSettings(w http.ResponseWriter, r *http.Request) {
	var changes store.OIDCChanges
	if !decode(w, r, &changes) {
		return
	}
	saved, e := a.saveOIDCSettings(r.Context(), changes)
	if e != nil {
		status, code, _ := oidcSettingsError(e)
		fail(w, status, code)
		return
	}
	JSON(w, 200, saved)
}
func (a *API) oidcSettingsForm(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fail(w, 400, "invalid_form")
		return
	}
	revision, e := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if e != nil {
		fail(w, 400, "invalid_form")
		return
	}
	changes := store.OIDCChanges{Enabled: r.FormValue("enabled") == "on", Issuer: r.FormValue("issuer"), ClientID: r.FormValue("client_id"), ClientSecret: r.FormValue("client_secret"), ClearSecret: r.FormValue("clear_secret") == "on", RoleClaim: r.FormValue("role_claim"), Revision: revision, RoleMap: map[string]string{}}
	for _, role := range []string{"manager", "admin"} {
		for _, name := range strings.Split(r.FormValue(role+"_groups"), ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, exists := changes.RoleMap[name]; exists {
				a.renderOIDCFormError(w, r, changes, store.ErrInvalidOIDC)
				return
			}
			changes.RoleMap[name] = role
		}
	}
	if _, e = a.saveOIDCSettings(r.Context(), changes); e != nil {
		a.renderOIDCFormError(w, r, changes, e)
		return
	}
	http.Redirect(w, r, "/settings?tab=oidc&oidc=saved", 303)
}
func (a *API) renderOIDCFormError(w http.ResponseWriter, r *http.Request, v store.OIDCChanges, e error) {
	status, _, message := oidcSettingsError(e)
	data, dbErr := a.settingsData(r)
	if dbErr != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	// Never repopulate the client secret, even after validation failure.
	data["OIDC"] = store.OIDCSettings{Enabled: v.Enabled, Issuer: v.Issuer, ClientID: v.ClientID, RoleClaim: v.RoleClaim, RoleMap: v.RoleMap, Revision: v.Revision, SecretSet: data["OIDC"].(store.OIDCSettings).SecretSet}
	data["OIDCAdminGroups"] = oidcGroups(v.RoleMap, "admin")
	data["OIDCManagerGroups"] = oidcGroups(v.RoleMap, "manager")
	data["SettingsTab"] = "oidc"
	data["OIDCError"] = web.Translate(Principal(r).User.Language, message)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	a.Web.Render(w, "settings", data)
}
func oidcGroups(mapping map[string]string, role string) string {
	values := []string{}
	for name, mapped := range mapping {
		if mapped == role {
			values = append(values, name)
		}
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}
