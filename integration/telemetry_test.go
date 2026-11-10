package integration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"vartovyi/internal/bus"
	"vartovyi/internal/model"
	"vartovyi/internal/store"
	"vartovyi/internal/telemetry"
)

func TestTraceContextThroughDurableOutboxAndJetStream(t *testing.T) {
	if os.Getenv("VARTOVYI_INTEGRATION") != "1" {
		t.Skip("requires disposable Compose services")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const db = "postgres://vartovyi:test-password@postgres:5432/vartovyi?sslmode=disable"
	const key = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	admin, err := store.Open(ctx, db, key)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	// Isolate outbox rows from the concurrently running recorder relay.
	const schema = "telemetry_acceptance"
	if _, err = admin.Pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	s, err := store.Open(ctx, db+"&search_path="+schema, key)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	spans := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(spans))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	defer func() { _ = p.Shutdown(context.Background()); otel.SetTracerProvider(previous) }()
	parent, root := telemetry.Start(ctx, "synthetic motion", trace.SpanKindServer)
	event := model.Event{ID: model.ID(), CameraID: "trace-camera", ShardID: "main", Version: 1, Type: "motion", At: time.Now(), End: time.Now().Add(time.Second)}
	if err = s.Event(parent, event); err != nil {
		t.Fatal(err)
	}
	root.End()
	var carrier map[string]string
	if err = s.Pool.QueryRow(ctx, "SELECT trace_context FROM event_outbox WHERE id=$1", event.ID).Scan(&carrier); err != nil {
		t.Fatal(err)
	}
	if trace.SpanContextFromContext(telemetry.Extract(ctx, carrier)).TraceID() != root.SpanContext().TraceID() {
		t.Fatal("database did not preserve parent")
	}
	subject := "vartovyi.v1.trace-test." + event.ID
	if _, err = s.Pool.Exec(ctx, "UPDATE event_outbox SET subject=$1 WHERE id=$2", subject, event.ID); err != nil {
		t.Fatal(err)
	}
	// A fresh Store proves relay propagation does not rely on process memory.
	s.Close()
	s, err = store.Open(ctx, db+"&search_path="+schema, key)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := bus.Open("nats://nats:4222")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	sub, err := b.JS.PullSubscribe(subject, "", nats.BindStream("VARTOVYI"), nats.ManualAck())
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	if err = b.Outbox(ctx, s); err != nil {
		t.Fatal(err)
	}
	messages, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	msg := messages[0]
	consumerCtx, consumer := telemetry.Start(bus.MessageContext(ctx, msg), "synthetic consume", trace.SpanKindConsumer)
	consumer.End()
	if trace.SpanContextFromContext(consumerCtx).TraceID() != root.SpanContext().TraceID() {
		t.Fatal("JetStream lost trace ID")
	}
	if err = msg.AckSync(); err != nil {
		t.Fatal(err)
	}
	var producer sdktrace.ReadOnlySpan
	var persisted sdktrace.ReadOnlySpan
	for _, span := range spans.Ended() {
		if span.Name() == "motion persist" {
			persisted = span
		}
		if span.SpanKind() == trace.SpanKindProducer {
			producer = span
		}
	}
	if producer == nil || persisted == nil || producer.Parent().SpanID() != persisted.SpanContext().SpanID() || persisted.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Fatal("producer lost durable parent")
	}
	if msg.Header.Get("traceparent") == "" || strings.Contains(string(msg.Data), "trace_context") {
		t.Fatal("trace metadata must be headers only")
	}
	var decoded model.Event
	if err = json.Unmarshal(msg.Data, &decoded); err != nil || decoded.ID != event.ID {
		t.Fatal("public event payload changed")
	}
}
