package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"vartovyi/internal/api"
	"vartovyi/internal/bus"
	"vartovyi/internal/config"
	"vartovyi/internal/detector"
	"vartovyi/internal/notifier"
	"vartovyi/internal/recorder"
	run "vartovyi/internal/runtime"
	"vartovyi/internal/store"
	"vartovyi/internal/telemetry"
)

func Main(service string) {
	ctx, cancel := run.Context()
	defer cancel()
	if e := start(ctx, service); e != nil {
		slog.Error("service stopped", "service", service, "reason", "configuration, dependency, or listener failure; inspect readiness and configuration")
		os.Exit(1)
	}
}
func start(ctx context.Context, service string) error {
	c, e := config.Load()
	if e != nil {
		return e
	}
	shutdown, e := telemetry.Setup(ctx, service, c.ShardID)
	if e != nil {
		return e
	}
	defer shutdown()
	if service == "detector" {
		d := detector.New(c)
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); d.Run(workerCtx) }()
		defer func() { cancel(); <-done }()
		return run.Serve(ctx, c.Listen, d.Handler())
	}
	s, e := store.Open(ctx, c.DatabaseURL, c.EncryptionKey)
	if e != nil {
		return e
	}
	defer s.Close()
	switch service {
	case "migrate":
		for ctx.Err() == nil {
			if e = s.Migrate(ctx); e == nil {
				if c.AdminPassword != "" {
					return s.Bootstrap(ctx, c.AdminUser, c.AdminPassword)
				}
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
	case "api":
		for ctx.Err() == nil {
			if e = s.Ping(ctx); e == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
		a, e := api.New(ctx, c, s)
		if e != nil {
			return e
		}
		go func() {
			t := time.NewTicker(time.Hour)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					_ = s.Cleanup(ctx)
				}
			}
		}()
		return run.Serve(ctx, c.Listen, a.Handler())
	case "recorder":
		r, e := recorder.New(c, s)
		if e != nil {
			return e
		}
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); r.Run(workerCtx) }()
		defer func() { cancel(); <-done; r.Close() }()
		go relay(workerCtx, c.NATSURL, s)
		return run.Serve(ctx, c.Listen, r.Handler())
	case "notifier":
		n := notifier.New(c, s)
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); n.Run(workerCtx) }()
		defer func() { cancel(); <-done }()
		return run.Serve(ctx, c.Listen, n.Handler())
	}
	return errors.New("unknown service")
}
func relay(ctx context.Context, url string, s *store.Store) {
	for ctx.Err() == nil {
		b, e := bus.Open(url)
		if e == nil {
			for ctx.Err() == nil {
				c, cancel := context.WithTimeout(ctx, 10*time.Second)
				e = b.Outbox(c, s)
				cancel()
				if e != nil && !b.Conn.IsConnected() {
					break
				}
				select {
				case <-ctx.Done():
					b.Close()
					return
				case <-time.After(time.Second):
				}
			}
			b.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
