// Package telemetry provides optional, privacy-preserving distributed tracing.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Propagator excludes arbitrary baggage, which may contain personal data.
var Propagator = propagation.TraceContext{}

func Setup(ctx context.Context, service, shard string) (func(), error) {
	otel.SetTextMapPropagator(Propagator)
	if os.Getenv("OTEL_SDK_DISABLED") == "true" || (os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "") {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func() {}, nil
	}
	sampler, err := samplerFromEnv()
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"} {
		if v := os.Getenv(key); v != "" && v != "http/protobuf" {
			return nil, errors.New("tracing supports OTLP http/protobuf only")
		}
	}
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithTimeout(3*time.Second),
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: true, InitialInterval: time.Second, MaxInterval: 2 * time.Second, MaxElapsedTime: 5 * time.Second}))
	if err != nil {
		return nil, errors.New("invalid OTLP tracing configuration")
	}
	name := os.Getenv("OTEL_SERVICE_NAME")
	if name == "" {
		name = "vartovyi-" + service
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sampler),
		sdktrace.WithResource(resource.NewWithAttributes("", attribute.String("service.name", name), attribute.String("vartovyi.shard.id", shard))),
		sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(256), sdktrace.WithMaxExportBatchSize(64), sdktrace.WithBatchTimeout(2*time.Second), sdktrace.WithExportTimeout(5*time.Second)))
	// Never print exporter errors: they may include endpoint credentials.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) { slog.Warn("telemetry export failed") }))
	otel.SetTracerProvider(provider)
	return func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if provider.Shutdown(c) != nil {
			slog.Warn("telemetry shutdown incomplete")
		}
	}, nil
}

func samplerFromEnv() (sdktrace.Sampler, error) {
	ratio := 0.1
	if raw := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); raw != "" {
		var err error
		ratio, err = strconv.ParseFloat(raw, 64)
		if err != nil || !(ratio >= 0 && ratio <= 1) {
			return nil, errors.New("OTEL_TRACES_SAMPLER_ARG must be between 0 and 1")
		}
	}
	switch os.Getenv("OTEL_TRACES_SAMPLER") {
	case "", "parentbased_traceidratio":
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio)), nil
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(ratio), nil
	case "always_on":
		return sdktrace.AlwaysSample(), nil
	case "always_off":
		return sdktrace.NeverSample(), nil
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample()), nil
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample()), nil
	default:
		return nil, errors.New("unsupported OTEL_TRACES_SAMPLER")
	}
}

func Start(ctx context.Context, name string, kind trace.SpanKind) (context.Context, trace.Span) {
	return otel.Tracer("vartovyi").Start(ctx, name, trace.WithSpanKind(kind))
}

// End deliberately omits raw errors, which may contain SQL values or URLs.
func End(span trace.Span, err error) {
	if err != nil {
		span.SetStatus(codes.Error, "operation failed")
	}
	span.End()
}

func Carrier(ctx context.Context) map[string]string {
	c := propagation.MapCarrier{}
	Propagator.Inject(ctx, c)
	return c
}

func Extract(ctx context.Context, carrier map[string]string) context.Context {
	c := propagation.MapCarrier{}
	for _, key := range []string{"traceparent", "tracestate"} {
		if value := carrier[key]; len(value) <= 512 {
			c[key] = value
		}
	}
	return Propagator.Extract(ctx, c)
}
