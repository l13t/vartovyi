package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func recorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(r))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()); otel.SetTracerProvider(previous) })
	return r
}

func TestHTTPPropagationStreamingAndPrivacy(t *testing.T) {
	spans := recorder(t)
	var received trace.SpanContext
	s := httptest.NewServer(HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = trace.SpanContextFromContext(r.Context())
		if _, ok := w.(http.Flusher); !ok {
			t.Error("lost streaming support")
		}
		w.WriteHeader(503)
		_, _ = w.Write([]byte("private-body"))
	})))
	defer s.Close()
	ctx, root := Start(t.Context(), "test root", trace.SpanKindInternal)
	req, _ := http.NewRequestWithContext(ctx, "GET", s.URL+"/botprivate-token?password=private-password", nil)
	req.Header.Set("Authorization", "Bearer private-header")
	client := &http.Client{Transport: Transport{Propagate: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	root.End()
	if received.TraceID() != root.SpanContext().TraceID() {
		t.Fatal("HTTP lost trace context")
	}
	ended := spans.Ended()
	if len(ended) != 3 {
		t.Fatalf("expected root/client/server, got %d", len(ended))
	}
	var clientSpan, serverSpan sdktrace.ReadOnlySpan
	for _, s := range ended {
		if s.SpanKind() == trace.SpanKindClient {
			clientSpan = s
		}
		if s.SpanKind() == trace.SpanKindServer {
			serverSpan = s
		}
		if strings.Contains(fmt.Sprint(s.Attributes(), s.Events(), s.Status(), s.Name()), "private-") {
			t.Fatal("secret leaked into HTTP span")
		}
	}
	if serverSpan.Parent().SpanID() != clientSpan.SpanContext().SpanID() {
		t.Fatal("server is not child of client span")
	}
	if clientSpan.Status().Code != codes.Error || serverSpan.Status().Code != codes.Error {
		t.Fatal("missing HTTP error status")
	}
}

func TestExternalTransportDoesNotPropagate(t *testing.T) {
	spans := recorder(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("traceparent") != "" {
			t.Error("propagated to external service")
		}
	}))
	defer s.Close()
	resp, err := (&http.Client{Transport: Transport{}}).Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(spans.Ended()) != 1 {
		t.Fatal("client span did not end on close")
	}
}

func TestProbesAndInvalidCarriers(t *testing.T) {
	spans := recorder(t)
	h := HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	if len(spans.Ended()) != 0 {
		t.Fatal("probes generated spans")
	}
	for _, carrier := range []map[string]string{nil, {"traceparent": "bad"}, {"traceparent": strings.Repeat("a", 1000), "baggage": "secret"}} {
		if trace.SpanContextFromContext(Extract(t.Context(), carrier)).IsValid() {
			t.Fatal("accepted invalid context")
		}
	}
}

func TestDatabasePrivacyAndErrors(t *testing.T) {
	spans := recorder(t)
	q := QueryTracer{}
	ctx := q.TraceQueryStart(t.Context(), nil, pgx.TraceQueryStartData{SQL: "SELECT private-secret", Args: []any{"private-password"}})
	q.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("private-error")})
	s := spans.Ended()[0]
	if s.Status().Code != codes.Error || strings.Contains(fmt.Sprint(s.Attributes(), s.Events(), s.Status()), "private-") {
		t.Fatal("database error lost or secrets exported")
	}
}

func TestOTLPExportAndShutdown(t *testing.T) {
	var mu sync.Mutex
	var received collector.ExportTraceServiceRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" || r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Error("incorrect OTLP request")
		}
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		mu.Lock()
		defer mu.Unlock()
		if err := proto.Unmarshal(b, &received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	for _, key := range []string{"OTEL_SDK_DISABLED", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "OTEL_SERVICE_NAME"} {
		t.Setenv(key, "")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1")
	previous := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	shutdown, err := Setup(t.Context(), "test", "main")
	if err != nil {
		t.Fatal(err)
	}
	_, span := Start(t.Context(), "export test", trace.SpanKindInternal)
	span.End()
	shutdown()
	mu.Lock()
	defer mu.Unlock()
	if len(received.ResourceSpans) != 1 || len(received.ResourceSpans[0].ScopeSpans[0].Spans) != 1 {
		t.Fatal("shutdown did not flush OTLP spans")
	}
	if !strings.Contains(fmt.Sprint(received.ResourceSpans[0].Resource), "vartovyi-test") {
		t.Fatal("missing service resource")
	}
}

func TestSamplingAndDisabledMode(t *testing.T) {
	for _, value := range []string{"NaN", "-1", "1.1", "bad"} {
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", value)
		if _, err := samplerFromEnv(); err == nil {
			t.Fatalf("accepted invalid ratio %q", value)
		}
	}
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0")
	t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
	sampler, err := samplerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	remote := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled, Remote: true})
	if sampler.ShouldSample(sdktrace.SamplingParameters{ParentContext: trace.ContextWithRemoteSpanContext(t.Context(), remote)}).Decision != sdktrace.RecordAndSample {
		t.Fatal("ignored sampled parent")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	previous := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	shutdown, err := Setup(t.Context(), "test", "main")
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()
	_, span := Start(t.Context(), "disabled", trace.SpanKindInternal)
	defer span.End()
	if span.IsRecording() {
		t.Fatal("tracing enabled without endpoint")
	}
}

func TestCarrierRoundTrip(t *testing.T) {
	recorder(t)
	ctx, span := Start(t.Context(), "persist", trace.SpanKindInternal)
	defer span.End()
	carrier := Carrier(ctx)
	carrier["baggage"] = "secret"
	restored := Extract(t.Context(), carrier)
	if trace.SpanContextFromContext(restored).TraceID() != span.SpanContext().TraceID() {
		t.Fatal("persisted carrier lost trace ID")
	}
	if len(Carrier(restored)) != 1 {
		t.Fatal("unexpected propagated metadata")
	}
}

func TestRegisteredRouteDoesNotExposeIdentifiers(t *testing.T) {
	spans := recorder(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cameras/{id}", func(w http.ResponseWriter, r *http.Request) {})
	HTTPHandler(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/cameras/private-camera?token=private-token", nil))
	span := spans.Ended()[0]
	if span.Name() != "GET /cameras/{id}" || strings.Contains(fmt.Sprint(span.Attributes()), "private-") {
		t.Fatal("route leaked request values")
	}
}

func TestCollectorFailureDoesNotBlockRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	for _, key := range []string{"OTEL_SDK_DISABLED", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"} {
		t.Setenv(key, "")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1")
	previous := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	shutdown, err := Setup(t.Context(), "test", "main")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	h := HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for range 1000 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/operation", nil))
		if w.Code != 204 {
			t.Fatal("collector failure changed application response")
		}
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("export queue blocked request handling")
	}
	started = time.Now()
	shutdown()
	if time.Since(started) > 6*time.Second {
		t.Fatal("unbounded shutdown")
	}
}
