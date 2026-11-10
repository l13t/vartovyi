# Metrics and distributed tracing

## Prometheus

API and recorder expose authenticated `GET /metrics`. Use the internal service
token as a Bearer credential from a private credentials file. Existing dashboards
and alert rules are under `monitoring/`. OpenTelemetry does not replace these
metrics, and does not export metrics or logs over OTLP.

## OpenTelemetry traces

Tracing is disabled unless an OTLP endpoint is explicitly configured. Each Go
service initializes its own provider (`vartovyi-api`, `vartovyi-recorder`,
`vartovyi-detector`, `vartovyi-notifier`, `vartovyi-migrate`). MediaMTX, FFmpeg and
the short-lived recording hook are not instrumented. Set environment variables
on the application services, for example:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_TRACES_SAMPLER=parentbased_traceidratio
OTEL_TRACES_SAMPLER_ARG=0.1
```

The base endpoint receives `/v1/traces`. Alternatively,
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` specifies the full trace endpoint. Only
OTLP/HTTP with binary protobuf is supported. HTTPS uses certificate verification;
the exporter also supports standard OTLP certificate and header environment
variables. Keep headers/credentials in private deployment secrets. A Collector
can forward traces to Tempo, Jaeger or another OTLP backend; none is deployed
automatically. `OTEL_SERVICE_NAME` overrides the service name and
`OTEL_SDK_DISABLED=true` disables tracing even with an endpoint.

Nomad Pack exposes `otel_endpoint` (empty by default) and `otel_sample_ratio`
(`"0.1"` by default). The collector must be reachable from each allocation;
`localhost` refers to the allocation, not the Nomad host. For example:

```hcl
otel_endpoint = "http://collector.internal:4318"
otel_sample_ratio = "0.1"
```

Root traces default to 10% sampling, respecting a parent's decision. Supported
samplers are `parentbased_traceidratio`, `traceidratio`, `always_on`, `always_off`,
`parentbased_always_on` and `parentbased_always_off`. Ratios must be between 0 and
1. Use `always_on` temporarily for synthetic acceptance tests.

## Coverage and privacy

- HTTP server/client spans include method, registered route pattern, status and duration. Streaming media
  spans finish when the body closes. Probe and `/metrics` requests are excluded.
- API-to-recorder proxy calls and detector-to-recorder requests propagate W3C
  trace context. Notifier snapshot requests also propagate; Telegram and MediaMTX
  requests create client spans without forwarding context to those services.
- PostgreSQL query spans use pgx's query tracer. SQL text, query parameters,
  connection strings and raw errors are never exported. Batch/copy operations
  are not separately instrumented.
- Motion's originating trace context is stored in the private event manifest,
  then in a separate outbox column. The relay restores it and adds W3C headers
  to JetStream messages. Each notifier attempt creates a consumer span, including
  redeliveries. Deduplication and acknowledgment behavior are unchanged.
- Old manifests/outbox rows without context still work and start new traces.
  The public JSON event payload and OpenAPI contract do not contain trace data.
- Raw URLs, headers, cookies, request/response bodies, camera credentials, SQL values,
  Telegram tokens and arbitrary baggage are excluded. Raw errors are represented
  by a generic failure status. Trace/span IDs are correlation metadata.

The exporter uses a nonblocking queue of 256 spans, batches of up to 64 and
bounded HTTP/export deadlines. Under collector failure, telemetry can be dropped;
it never blocks recording waiting for queue capacity. Shutdown attempts to flush
for up to five seconds after workers and HTTP requests finish. Traces are
diagnostics, not a durable audit log. Review backend retention/access separately
from recorded-video retention.
