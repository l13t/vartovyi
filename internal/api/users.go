package api

import (
	"errors"
	"net/http"
	"vartovyi/internal/model"
	"vartovyi/internal/store"
)

func (a *API) deleteUsers(w http.ResponseWriter, r *http.Request) {
	var v struct {
		IDs []string `json:"ids"`
	}
	if !decode(w, r, &v) {
		return
	}
	count, e := a.Store.DeleteUsers(r.Context(), Principal(r).User.ID, v.IDs)
	if e != nil {
		switch {
		case errors.Is(e, store.ErrDeleteSelf):
			fail(w, 400, "cannot_delete_self")
		case errors.Is(e, store.ErrLastAdmin):
			fail(w, 400, "cannot_delete_last_admin")
		case errors.Is(e, store.ErrInvalidSelection):
			fail(w, 400, "invalid_user_selection")
		case errors.Is(e, store.ErrNotFound):
			fail(w, 404, "user_not_found")
		case errors.Is(e, store.ErrForbidden):
			fail(w, 403, "forbidden")
		default:
			fail(w, 503, "database_unavailable")
		}
		return
	}
	JSON(w, 200, map[string]int{"deleted": count})
}
func (a *API) userEditorPage(w http.ResponseWriter, r *http.Request) {
	data := a.pageData(r)
	u := model.User{Role: model.Viewer}
	if id := r.PathValue("id"); id != "" {
		var e error
		u, e = a.Store.User(r.Context(), id)
		if e != nil {
			fail(w, 404, "user_not_found")
			return
		}
	}
	data["Account"] = u
	a.Web.Render(w, "user-editor", data)
}
