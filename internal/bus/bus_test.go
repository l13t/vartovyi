package bus

import (
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"
	"testing"
)

func TestMessageContextAcceptsNATSHeaderCasing(t *testing.T) {
	for _, key := range []string{"traceparent", "Traceparent", "TraceParent"} {
		msg := nats.NewMsg("vartovyi.v1.motion")
		msg.Header.Set(key, "00-01000000000000000000000000000000-0100000000000000-01")
		ctx := MessageContext(t.Context(), msg)
		if !trace.SpanContextFromContext(ctx).IsValid() {
			t.Fatalf("lost %s", key)
		}
	}
}
