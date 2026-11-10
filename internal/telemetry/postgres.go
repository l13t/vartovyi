package telemetry

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// QueryTracer never records SQL text, parameters, DSNs or raw database errors.
type QueryTracer struct{}

func (QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	ctx, span := Start(ctx, "postgresql query", trace.SpanKindClient)
	span.SetAttributes(attribute.String("db.system.name", "postgresql"))
	return ctx
}
func (QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	err := data.Err
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	End(trace.SpanFromContext(ctx), err)
}
