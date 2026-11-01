---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
---

# Observability

`last-mile-delivery` follows the fleet observability standard ([ADR 0008](/docs/adr/0008-fleet-observability-standard)): JSON logs on stdout, traces and metrics pushed over OTLP/gRPC to the OpenTelemetry Collector, a trace that continues across Kafka. All three binaries (`cmd/api`, `cmd/mcp`, `cmd/a2a`) bring telemetry up the same way, before the router is built, and flush it last on shutdown (5 s bound).

## Exporter configuration

| Variable | Default | Effect |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC endpoint, `host:port` or URL. In the kind cluster the chart sets `otel-collector.observability.svc.cluster.local:4317`. |
| `OTEL_SERVICE_NAME` | `last-mile-delivery` (`-mcp`, `-a2a` for those binaries) | `service.name`; also the Loki `app` label source. |
| `SERVICE_VERSION` | `dev` | `service.version`. |
| `ENVIRONMENT` | `local` | `deployment.environment.name`. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`/`warning`, `error`. |

Export is non-blocking: the gRPC exporters connect lazily, so a Collector that is down or absent costs telemetry, never availability or startup. Exporter errors are logged at DEBUG only. The metric reader pushes every 15 s. Context propagation is W3C `traceparent` plus baggage. The chart's `otel.*` values render these variables (`otel.enabled: false` renders none, and the binaries then fall back to `localhost:4317`). Source: `internal/adapters/outbound/telemetry/telemetry.go`, `cmd/*/telemetry.go`.

## Metrics

| Instrument | Type | Unit | Attributes | Emitted by | Meaning |
| --- | --- | --- | --- | --- | --- |
| `last_mile_delivery.package.attempted` | Int64 counter | `{package}` | `outcome` = `delivered` \| `failed` \| `rejected`; `reason` only when `rejected` (a problem slug such as `route-not-started`, `already-delivered`, `internal-error`) | `cmd/api` (RecordAttempt use case) | Every delivery attempt a driver reports. Prometheus name `last_mile_delivery_package_attempted_total`. A rising `rejected` share means clients report attempts the route cannot take; a rising `internal-error` reason means the service itself is failing. |
| `http.server.request.duration` | histogram | `s` | route pattern, method, status code (from `otelchimetric`) | all three binaries | Request duration per chi route pattern (never per id). Prometheus name `http_server_request_duration_seconds_*` with `service_name` and `http_route` labels. |
| Go runtime metrics (`go.goroutine.count`, `go.memory.used`, GC and heap series) | gauges / counters | various | none | all three binaries | From `go.opentelemetry.io/contrib/instrumentation/runtime`. |

The business counter is registered under the meter scope `github.com/claudioed/last-mile-delivery` and is bound after `telemetry.Setup`, so it reaches the real provider (`internal/adapters/outbound/telemetry/attempt_metrics.go`). The service serves no `/metrics` endpoint; metrics are pushed, not scraped.

## Traces

- **HTTP server span** per request, created by `otelchi` and named after the chi route pattern (one span name per route, not per wave or route id). Same on `cmd/mcp` and `cmd/a2a`.
- **Outbox propagation**: when a use case enqueues an event, the active span context is injected into the outbox row's `headers` (`traceparent`, `tracestate`) in the same transaction, and the relay publishes them as ordinary Kafka headers (`internal/adapters/outbound/postgres/outbox_trace.go`). The CloudEvents attributes and body are untouched.
- **Consumer span** `<topic> process` (kind `CONSUMER`, attributes `messaging.system=kafka`, `messaging.destination.name`, `messaging.operation.name=process`), parented to the trace in the incoming message headers; a message without them starts a new trace. A returned error is recorded on the span (`internal/adapters/inbound/kafka/tracing.go`). Events a consumer enqueues continue the same trace.
- Traces land in Jaeger through the Collector.

## Logs

JSON via `slog` on stdout; a handler wrapper stamps `trace_id` and `span_id` on every record written with a span-carrying context (`internal/adapters/outbound/telemetry/slogotel.go`). The SDK's own diagnostics are bridged onto the same handler.

| Record | Level | Fields |
| --- | --- | --- |
| `http request` (access log, `internal/adapters/inbound/requestlog`) | INFO, 4xx WARN, 5xx ERROR | `method`, `route` (pattern, `unmatched` when none), `status`, `duration_ms`, `bytes`, `request_id`, plus `trace_id`, `span_id`. `/healthz` and `/readyz` are not logged. |
| `consumer running` | INFO | `consumer`, `topic`, `group_id`, `brokers` |
| `outbox relay running` | INFO | `interval`, `max_attempts`, `topic` |
| `... handling failed; retrying the same message` | ERROR | `error`, `partition`, `offset`, `attempt`, `max_attempts`, `retry_in` |
| `... dead-lettering ...` | WARN / ERROR | `consumer`, `type`, `event_id`, `error` |
| `outbox relay pass failed` | ERROR | `error` |
| `outbox relay: row reached the attempt cap ...` | ERROR | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `max_attempts`, `error` |
| `readiness: dependency is not ready` | WARN | `dependency`, `error` |
| `shutdown: readiness flipped to not-ready; waiting for traffic to drain` | INFO | `drain_delay` |

Loki label: `app=last-mile-delivery` (the API pod); filter out the Istio sidecar with `container!~"istio-proxy|istio-init"`.

## Dashboards and alerts that exist

From `IQVO/warehouse-infra` on `develop`:

- **Grafana dashboard** `warehouse-last-mile-delivery` ("Warehouse — Last-Mile Delivery", `terraform/dashboards/contexts/last-mile-delivery.json`): Kong request rate, 5xx ratio and p95 latency for the context's routes; service HTTP request rate and p95 by route; the business panel `last_mile_delivery_package_attempted_total` by `outcome`; Go goroutines and memory; live logs, log volume by level and an errors/warnings-only stream.
- **Alert rules** labelled `context="last-mile-delivery"` (`terraform/alerting/warehouse-alert-rules.json`): `WarehouseKong5xxRatioHigh` (warning, over 5 % 5xx for 5 m), `WarehousePodRestarting` (warning, at least 2 restarts in 15 m on `last-mile-delivery-*` pods), `WarehouseApiAvailabilityBurnFast` and `WarehouseApiLatencyBurnFast` (critical), `WarehouseApiAvailabilityBurnSlow` and `WarehouseApiLatencyBurnSlow` (warning).
- There is **no alert on the business counter**, on outbox backlog or on consumer lag. Watch the unpublished outbox row count with SQL (see the [Runbook](/docs/operations/runbook)).

The Prometheus panel for the counter stays empty until a `RecordAttempt` call has happened; an empty panel on a quiet station is not an outage.
