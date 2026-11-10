package telemetry

import (
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func method(r *http.Request) string {
	switch r.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return r.Method
	default:
		return "OTHER"
	}
}

// HTTPHandler preserves streaming interfaces and excludes scrape/probe traffic.
// No URLs, bodies, cookies or authorization headers are recorded.
func HTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := Propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := Start(ctx, "HTTP "+method(r), trace.SpanKindServer)
		defer span.End()
		span.SetAttributes(attribute.String("http.request.method", method(r)))
		request := r.WithContext(ctx)
		m := httpsnoop.CaptureMetrics(next, w, request)
		if pattern := request.Pattern; pattern != "" {
			_, route, ok := strings.Cut(pattern, " ")
			if !ok {
				route = pattern
			}
			span.SetName(method(r) + " " + route)
			span.SetAttributes(attribute.String("http.route", route))
		}
		span.SetAttributes(attribute.Int("http.response.status_code", m.Code))
		if m.Code >= 500 {
			span.SetStatus(codes.Error, "HTTP server error")
		}
	})
}

// Transport records only method/status. Propagate is for trusted service calls.
type Transport struct {
	Base      http.RoundTripper
	Propagate bool
}

func (t Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	ctx, span := Start(r.Context(), "HTTP "+method(r), trace.SpanKindClient)
	span.SetAttributes(attribute.String("http.request.method", method(r)))
	req := r.Clone(ctx)
	if t.Propagate {
		Propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		End(span, err)
		return resp, err
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, "HTTP request rejected")
	}
	resp.Body = &traceBody{ReadCloser: resp.Body, span: span}
	return resp, nil
}

type traceBody struct {
	io.ReadCloser
	span trace.Span
	once sync.Once
}

func (b *traceBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		spanErr := err
		if err == io.EOF {
			spanErr = nil
		}
		b.once.Do(func() { End(b.span, spanErr) })
	}
	return n, err
}
func (b *traceBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { End(b.span, err) })
	return err
}
