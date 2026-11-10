package api

import (
	"errors"
	"net/http"
	"vartovyi/internal/store"
)

func (a *API) getTelegram(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.Telegram(r.Context(), a.Config.TelegramToken, a.Config.TelegramChat)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	JSON(w, 200, v)
}
func (a *API) putTelegram(w http.ResponseWriter, r *http.Request) {
	var v store.TelegramChanges
	if !decode(w, r, &v) {
		return
	}
	saved, e := a.Store.SaveTelegram(r.Context(), v, a.Config.TelegramToken, a.Config.TelegramChat)
	if e != nil {
		if errors.Is(e, store.ErrInvalidTelegram) {
			fail(w, 400, "invalid_telegram_settings")
		} else {
			fail(w, 503, "database_unavailable")
		}
		return
	}
	JSON(w, 200, saved)
}
func (a *API) telegramForm(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fail(w, 400, "invalid_form")
		return
	}
	v := store.TelegramChanges{Enabled: r.FormValue("enabled") == "on", ChatID: r.FormValue("chat_id"), Token: r.FormValue("token"), ClearToken: r.FormValue("clear_token") == "on"}
	_, e := a.Store.SaveTelegram(r.Context(), v, a.Config.TelegramToken, a.Config.TelegramChat)
	if e != nil {
		if errors.Is(e, store.ErrInvalidTelegram) {
			http.Redirect(w, r, "/settings?tab=telegram&telegram=invalid", 303)
		} else {
			fail(w, 503, "database_unavailable")
		}
		return
	}
	http.Redirect(w, r, "/settings?tab=telegram&telegram=saved", 303)
}
