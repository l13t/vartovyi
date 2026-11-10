package runtime

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"vartovyi/internal/auth"
	"vartovyi/internal/telemetry"
)

func Context() (context.Context, context.CancelFunc) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
func Serve(ctx context.Context, addr string, h http.Handler) error {
	s := &http.Server{Addr: addr, Handler: telemetry.HTTPHandler(h), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	stopped := make(chan struct{})
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
		case <-stopped:
			return
		}
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = s.Shutdown(c)
	}()
	e := s.ListenAndServe()
	close(stopped)
	<-shutdownDone
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
func Internal(token string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" && !auth.Equal(r.Header.Get("Authorization"), "Bearer "+token) {
			http.Error(w, "unauthorized", 401)
			return
		}
		h.ServeHTTP(w, r)
	})
}
func Health(m *http.ServeMux, ready func() error) {
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	m.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if ready != nil && ready() != nil {
			http.Error(w, "not ready", 503)
			return
		}
		w.WriteHeader(200)
	})
}
