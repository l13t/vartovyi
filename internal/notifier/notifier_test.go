package notifier

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"vartovyi/internal/config"
	"vartovyi/internal/model"
	"vartovyi/internal/store"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSendUsesResolvedSettingsAndSanitizesErrors(t *testing.T) {
	n := New(config.Config{TelegramToken: "old-token", TelegramChat: "old-chat"}, nil)
	n.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.telegram.org" || r.URL.Path != "/bot123:replacement/sendMessage" {
			t.Fatal("incorrect Telegram endpoint")
		}
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Fatal(e)
		}
		if r.FormValue("chat_id") != "-100123" {
			t.Fatal("incorrect destination")
		}
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"ok":false,"description":"123:replacement"}`)), Header: make(http.Header)}, nil
	})
	err := n.send(context.Background(), model.Event{CameraID: "fake-camera"}, store.TelegramSettings{Token: "123:replacement", ChatID: "-100123", Enabled: true})
	if err == nil || strings.Contains(err.Error(), "replacement") {
		t.Fatal("delivery error must be sanitized")
	}
}
