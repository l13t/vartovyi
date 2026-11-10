package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"vartovyi/internal/model"
	"vartovyi/internal/store"
	"vartovyi/internal/web"
)

func (a *API) profileData(r *http.Request) (map[string]any, error) {
	u, e := a.Store.User(r.Context(), Principal(r).User.ID)
	if e != nil {
		return nil, e
	}
	data := a.pageData(r)
	data["User"] = u
	data["CanChangePassword"] = u.PasswordHash != ""
	data["Saved"] = r.URL.Query().Get("saved") == "1"
	return data, nil
}
func (a *API) profilePage(w http.ResponseWriter, r *http.Request) {
	data, e := a.profileData(r)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	a.Web.Render(w, "profile", data)
}
func (a *API) getProfile(w http.ResponseWriter, r *http.Request) {
	u, e := a.Store.User(r.Context(), Principal(r).User.ID)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	JSON(w, 200, map[string]any{"user": u, "can_change_password": u.PasswordHash != ""})
}
func (a *API) changeProfile(w http.ResponseWriter, r *http.Request, v model.ProfileChanges) (model.User, string, error) {
	u, e := a.Store.UpdateProfile(r.Context(), Principal(r).User.ID, v)
	if e != nil {
		return u, "", e
	}
	csrf := Principal(r).CSRF
	if v.NewPassword != "" {
		token, newCSRF, e := a.Store.NewSession(r.Context(), u)
		if e != nil {
			return u, "", e
		}
		csrf = newCSRF
		http.SetCookie(w, &http.Cookie{Name: "vartovyi_session", Value: token, Path: "/", HttpOnly: true, Secure: a.Config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 86400})
	}
	return u, csrf, nil
}
func profileError(e error) (int, string, string) {
	switch {
	case errors.Is(e, store.ErrInvalidLanguage):
		return 400, "unsupported_language", "Непідтримувана мова."
	case errors.Is(e, store.ErrCurrentPassword):
		return 400, "incorrect_current_password", "Поточний пароль неправильний."
	case errors.Is(e, store.ErrExternalPassword):
		return 400, "external_password", "Пароль цього облікового запису керується провайдером входу."
	case errors.Is(e, store.ErrInvalidProfile):
		return 400, "invalid_name", "Вкажи ім’я довжиною до 100 символів."
	case errors.Is(e, store.ErrWeakPassword):
		return 400, "weak_password", "Новий пароль має містити щонайменше 12 символів (до 1024 байтів)."
	default:
		return 503, "database_unavailable", "Не вдалося зберегти зміни. Спробуй ще раз."
	}
}
func (a *API) patchProfile(w http.ResponseWriter, r *http.Request) {
	var v model.ProfileChanges
	if !decode(w, r, &v) {
		return
	}
	u, csrf, e := a.changeProfile(w, r, v)
	if e != nil {
		status, code, _ := profileError(e)
		fail(w, status, code)
		return
	}
	JSON(w, 200, map[string]any{"user": u, "csrf_token": csrf})
}
func (a *API) profileForm(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fail(w, 400, "invalid_form")
		return
	}
	var v model.ProfileChanges
	if r.PathValue("section") == "name" {
		name := r.FormValue("name")
		v.Name = &name
	} else if r.PathValue("section") == "password" {
		v.CurrentPassword = r.FormValue("current_password")
		v.NewPassword = r.FormValue("new_password")
		if v.NewPassword == "" || v.NewPassword != r.FormValue("confirm_password") {
			a.profileFormError(w, r, "Новий пароль і підтвердження мають збігатися.")
			return
		}
	} else {
		http.NotFound(w, r)
		return
	}
	if _, _, e := a.changeProfile(w, r, v); e != nil {
		_, _, message := profileError(e)
		a.profileFormError(w, r, message)
		return
	}
	http.Redirect(w, r, "/profile?saved=1", http.StatusSeeOther)
}
func (a *API) profileFormError(w http.ResponseWriter, r *http.Request, message string) {
	data, e := a.profileData(r)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	data["Error"] = web.Translate(Principal(r).User.Language, message)
	a.Web.Render(w, "profile", data)
}

func (a *API) languageForm(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fail(w, 400, "invalid_form")
		return
	}
	language := r.FormValue("language")
	if _, _, e := a.changeProfile(w, r, model.ProfileChanges{Language: &language}); e != nil {
		status, code, _ := profileError(e)
		fail(w, status, code)
		return
	}
	a.languageCookie(w, language)
	target := "/profile"
	if referer, e := url.Parse(r.Referer()); e == nil && referer.Path != "" && strings.HasPrefix(referer.Path, "/") && !strings.HasPrefix(referer.Path, "//") {
		target = referer.RequestURI()
	}
	http.Redirect(w, r, target, 303)
}

func (a *API) languageCookie(w http.ResponseWriter, language string) {
	http.SetCookie(w, &http.Cookie{Name: "vartovyi_language", Value: language, Path: "/", HttpOnly: true, Secure: a.Config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 31536000})
}
