# ADR 0008: Adopt the fleet observability standard (logs, metrics, traces)

## Status

Accepted (2026-10-26).

## Context

last-mile-delivery logged JSON with `log/slog` and nothing else: no trace or
span id on a log line, no request id, no request log, no metrics, no traces, no
`traceparent` on the events it publishes, and no OpenTelemetry settings in the
Helm chart. The warehouse-systems services (inventory-storage, wes-work-planning)
already share one pipeline and one convention, recorded in their ADRs "standard
metrics convention" (inventory-storage ADR 0016, wes-work-planning ADR 0013):

- pods export OTLP/gRPC to the in-cluster Collector
  (`otel-collector.observability.svc.cluster.local:4317`), which feeds Jaeger
  (traces) and a Prometheus exporter (metrics);
- logs stay on stdout as JSON for Alloy and Loki.

Without the same wiring a package could not be followed from the REST call
through the Kafka event to the consumer in another service, a log line could not
be joined to its trace, and no dashboard could show attempt rate, errors or
latency. The alternatives weighed were a service-specific setup (rejected: every
repo should answer the same query the same way) and the OTel Go auto-instrumentation
agents (rejected: they cannot follow a trace across `segmentio/kafka-go`, which has
no instrumentation, nor name spans by the chi route pattern). This ADR is the same
decision as customer-ordering ADR 0007.

## Decision

Tier 1 (identical in every repo) and Tier 2 (this context's business metric):

1. **Telemetry adapter.** `internal/adapters/outbound/telemetry` holds `Setup`
   (OTLP/gRPC traces and metrics, W3C TraceContext + Baggage propagator, Go
   runtime metrics; exporters dial lazily, so no Collector means dropped
   telemetry, never a blocked boot or request; returns a shutdown func) and
   `WithTraceContext` (stamps `trace_id`/`span_id` on a log record written with
   a span-carrying context). Only the composition roots (`cmd/api`, `cmd/mcp`)
   and the other adapters import it; `internal/domain` and `internal/application`
   never import OpenTelemetry.
2. **Logger.** `slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, ...)))`,
   `LOG_LEVEL`, uppercase levels. Code on a request or message path uses the
   `*Context` slog variants (the MCP tool error path was the only bare call left
   on such a path); the bare variants have no span.
3. **HTTP middleware order** on both chi routers (REST and MCP):
   `RequestID` -> `otelchi.Middleware(serviceName, WithChiRoutes(r))` ->
   `otelchimetric.NewServerRequestDuration` -> `RequestLogger` -> `Recoverer`;
   the existing `rejectNUL` and Idempotency-Key middleware stay after them.
   `telemetry.Setup` runs before the router is built (otelchimetric binds the
   global MeterProvider at construction) and is flushed last on shutdown. The
   request line carries `method`, `route` (the chi pattern, never the raw path,
   which holds ids), `status`, `duration_ms`, `bytes` and `request_id`; 5xx is
   ERROR, 4xx WARN, the rest INFO; `/healthz` and `/readyz` are not logged.
   `RequestLogger` lives in `internal/adapters/inbound/requestlog`, not in the
   REST adapter, because `cmd/mcp` may not import the REST adapter and both
   routers must log identically.
4. **Traces over Kafka.** `postgres.Outbox.Append` stamps `traceparent`/`tracestate`
   from the active span into the outbox headers, inside the transaction that
   writes the aggregate (`internal/adapters/outbound/postgres/outbox_trace.go`).
   The relay publishes the headers unchanged as Kafka headers; each inbound
   consumer (linehaul, node-topology, shipping) extracts them and runs every
   handling attempt in a consumer span named `<topic> process`, so the use case's
   logs carry the publisher's trace id and an event enqueued while handling
   continues the trace. Only extra headers are added: the CloudEvents attributes
   and body, and the encoder's output, are unchanged (ADR 0004).
5. **Environment contract.** `cmd/api` and `cmd/mcp` read `OTEL_SERVICE_NAME`
   (defaults `last-mile-delivery` and `last-mile-delivery-mcp`),
   `OTEL_EXPORTER_OTLP_ENDPOINT` (default `telemetry.DefaultEndpoint`, a bare
   `host:port` or a URL), `SERVICE_VERSION`, `ENVIRONMENT` (read by `Setup`) and
   `LOG_LEVEL`.
6. **Chart.** `otel.enabled`, `otel.endpoint`, `otel.serviceName` render
   `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME` (`<name>-mcp` for the
   mcp component); `SERVICE_VERSION` is the image tag, or the chart appVersion
   when empty, for the api; the api also renders `ENVIRONMENT`. `extraEnv` is
   refused when it names one of these four. `charts/last-mile-delivery/tests/render_test.sh`
   asserts it and scans `cmd/` for the variables the binaries read.
7. **Tier 2 business metric.** One counter, `last_mile_delivery.package.attempted`,
   unit `{package}`, attribute `outcome` (`delivered` | `failed` | `rejected`) and,
   on a rejection, `reason` (the problem slug the REST adapter answers with:
   `route-not-started`, `package-not-on-route`, `already-delivered`,
   `attempt-already-recorded`, `illegal-transition`, `concurrent-update`,
   `not-found`, `validation-failed`, `internal-error`). `ports.AttemptMetrics` is
   the application port; `RecordAttempt` (the context's primary command: the
   driver's report of what happened at the door, which feeds the history, the
   re-induction and the return policies) calls it once per command; the OTel
   implementation is in the telemetry adapter and is injected from `cmd/api`; a
   nil `Deps.Metrics` is a no-op. The free-text failure reason is never an
   attribute (unbounded cardinality). It follows the fleet convention: dotted
   lowercase name, one counter with an `outcome` attribute rather than three
   counters, UCUM unit, a description that says what a human should conclude when
   it moves.

### Assumptions

- **O1.** The Tier 2 counter is on attempts, not on wave scheduling or route
  planning: the attempt outcome is where the context's value (a package reaching
  its recipient, ADR 0001) is decided, and the only place where a failure rate is
  meaningful. Waves and routes are planned by operators and refused with plain
  validation errors.
- **O2.** The MCP component keeps its `mcp.serviceVersion` default (`1.0.0`,
  reported in the MCP initialize handshake) instead of following the image tag as
  the reference does: changing it would alter what MCP clients see, which is out
  of scope. Its telemetry `service.version` therefore equals that value.
- **O3.** There is no `docs/runtime.md` in this repo; the environment is
  documented in the `cmd/api` and `cmd/mcp` package comments, the chart values and
  the README "Observability" section.
- **O4.** The existing optional `/metrics` handler field on the REST `Server` is
  left as it is; the OTLP pipeline does not use it.

Not part of this ADR: dashboards and alert rules, which live in warehouse-infra
and are generated per deployed context from the instrument names above.

## Consequences

- A request, its log lines, the events it enqueued and the consumers that handle
  them in other services share one trace id; `trace_id` in Loki jumps to Jaeger.
- Request rate, errors and duration come from `http.server.request.duration`
  (otelchimetric) with no hand-rolled counter; delivery health from
  `last_mile_delivery.package.attempted`.
- Every new log call on a request or message path must use a `*Context` variant,
  or its line carries no trace id.
- A consumer span is created per handling attempt, so a retried message shows as
  several spans under the same parent.
- The trace parent of a consumed event is the span that was active at the outbox
  write (the REST request or the upstream consumer span), not a separate publish
  span: the relay is asynchronous and adds no span of its own.
- `OutboxRepo.Insert` (the raw path used by tests and the repository contract)
  stamps nothing; only `Outbox.Append`, the path every use case takes, does.
