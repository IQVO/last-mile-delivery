---
id: configuration
title: Configuration
sidebar_label: Configuration
sidebar_position: 1
---

# Configuration

`last-mile-delivery` ships **three binaries** (`cmd/api`, `cmd/mcp`, `cmd/a2a`). All of them are configured only through environment variables: there is no config file, no flag and no secret-manager integration in the repository. REST, MCP and A2A are unauthenticated fleet-wide, so there is no token or key to configure.

The service runs on real Postgres (and, in `cmd/api`, real Kafka) only
([ADR 0005](/docs/adr/0005-runtime-outbox-http-and-readiness)): a missing mandatory variable is a boot error that names it, raised before the listener binds. Test-only variables do not exist in this repo (the integration tests start their own containers with testcontainers). The values below were read from `cmd/*/main.go`, `cmd/api/config.go`, `cmd/*/telemetry.go` and `internal/adapters/outbound/telemetry/telemetry.go`.

## `cmd/api`

The REST API, the three Kafka consumers and the outbox relay in one process.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server (also serves `/healthz` and `/readyz`). |
| `DATABASE_URL` | none | **yes** | Postgres DSN. Missing = boot error `DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode`. Embedded migrations run at boot; the outbox, the `Idempotency-Key` store and every repository live here. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled) DSN used only for the boot migrations, because golang-migrate's advisory lock does not survive PgBouncer transaction pooling. Empty = `DATABASE_URL`. |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated broker list (entries trimmed, blanks dropped). Missing = boot error `KAFKA_BROKERS is required: this context runs on real Kafka (outbox relay, consumers, /readyz)`. Used by the consumers, the outbox relay and the `/readyz` metadata check. In the fleet's kind cluster the single broker is `localhost:9092` from the host. |
| `LINEHAUL_CONSUMER_GROUP` | none | **yes** | Kafka consumer group id of the linehaul consumer (topic `retailnetwork.linehaul-transportation.events`, event `LoadArrived`). Missing = boot error `LINEHAUL_CONSUMER_GROUP is required: the consumer always runs on real Kafka`. A shared or defaulted group would let two services steal each other's offsets, so there is no default. |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | none | **yes** | Consumer group id of the node-topology consumer (topic `retailnetwork.node-topology.events`, events `NodeRegistered`, `ServiceAreaAssigned`). Same boot error shape. |
| `SHIPPING_CONSUMER_GROUP` | none | **yes** | Consumer group id of the shipping consumer (topic `retailnetwork.shipping-and-tracking.events`, event `LabelIssued`). Same boot error shape. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Pause between relay passes that did not fill a batch. A Go duration (`500ms`, `2s`) or plain seconds (`2`, `0.5`). Unparsable or non-positive values log a WARN and use the default. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures after which the relay stops claiming an outbox row. `0` = unlimited. Negative or unparsable values log a WARN and use `20`, so a typo never removes the cap. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | After SIGINT/SIGTERM `/readyz` flips to 503 and the process waits this long before closing the listener. `0` disables the wait. Negative or unparsable values log a WARN and use `5s`. Read when the signal arrives, not at boot. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive; anything else silently falls back to `info`. Logs are JSON on stdout. |
| `OTEL_SERVICE_NAME` | `last-mile-delivery` | no | `service.name` of spans and metrics, and the Loki `app` label. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint, `host:port` or a full URL. A bare `host:port` is normalised to `http://host:port` (and the variable is rewritten in place). Export never blocks: an unreachable Collector costs telemetry, not availability. |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. |

Boot validation reports the first problem only (`DATABASE_URL`, then `KAFKA_BROKERS`, then the three groups in the order above). There is no mode matrix: with the five mandatory variables set, the three consumers and the relay all start.

## `cmd/mcp`

Read-only MCP server (Streamable HTTP). It never dials Kafka, so it has no `KAFKA_BROKERS` and no consumer group.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address. Serves `/healthz`, and the MCP endpoint on both `/mcp` and `/`. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service database that `cmd/api` owns. Missing = boot error `DATABASE_URL is required: this context runs on real Postgres`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the (idempotent) boot migrations; this binary can boot against a fresh database before `cmd/api` has. |
| `SERVICE_VERSION` | `1.0.0` for the MCP server version, `dev` for `service.version` | no | The two are read separately: MCP clients see `1.0.0` unless set; telemetry shows `dev` unless set. |
| `ENVIRONMENT` | unset (logged), `local` for the telemetry attribute | no | `deployment.environment.name`; also logged at boot. |
| `LOG_LEVEL` | `info` | no | As `cmd/api`. |
| `OTEL_SERVICE_NAME` | `last-mile-delivery-mcp` | no | `service.name`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | As `cmd/api`. |

## `cmd/a2a`

Read-only A2A (Agent2Agent) agent, JSON-RPC 2.0 over HTTP. It never dials Kafka.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `A2A_ADDR` | `:8093` | no | Listen address. Serves `/healthz`, `/.well-known/agent-card.json`, and JSON-RPC on `/` and `/a2a`. |
| `A2A_AGENT_URL` | none | **yes** | Absolute `http(s)` URL (with a host) that the Agent Card advertises for the JSON-RPC endpoint, for example `http://last-mile-delivery-a2a.warehouse-systems.svc.cluster.local:8093/`. Missing or not absolute = boot error before telemetry, any connection and the listener. |
| `DATABASE_URL` | none | **yes** | Postgres DSN. Missing = boot error `DATABASE_URL is required: this context runs on real Postgres`. With a bad `A2A_AGENT_URL` as well, both problems are reported together (`errors.Join`). |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations. |
| `SERVICE_VERSION` | `1.0.0` for the Agent Card, `dev` for `service.version` | no | Read separately, as in `cmd/mcp`. |
| `ENVIRONMENT` | unset (logged), `local` for the telemetry attribute | no | `deployment.environment.name`; logged at boot with `agent_url`. |
| `LOG_LEVEL` | `info` | no | As `cmd/api`. |
| `OTEL_SERVICE_NAME` | `last-mile-delivery-a2a` | no | `service.name`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | As `cmd/api`. |

## Readiness

`GET /readyz` exists on `cmd/api` only. It is 200 only when the shutdown drain gate is open **and** Postgres answers a ping **and** a Kafka broker answers a metadata request that names no topic (reachability only). The verdict is cached for 3 s and one round of checks is bounded by 2 s. A failed dependency is a `503` `application/problem+json` of type `dependency-unavailable` with detail `not ready: kafka` or `not ready: postgres`. `/healthz` is liveness only and exists on all three binaries. `cmd/mcp` and `cmd/a2a` have no `/readyz`. Topics are not part of readiness: the broker creates them on first write.

## Compiled-in settings (not configurable)

These are constants in the source; changing them needs a code change.

| Setting | Value | Source |
| --- | --- | --- |
| HTTP `ReadHeaderTimeout` (all three binaries) | 5 s | `cmd/*/main.go` |
| HTTP graceful shutdown timeout | 10 s (`cmd/api` also waits at most 10 s per worker) | `cmd/api/main.go` (`shutdownTimeout`, `workerDrainTimeout`), `cmd/mcp/main.go`, `cmd/a2a/main.go` |
| Telemetry flush on shutdown | 5 s | `cmd/api/telemetry.go` |
| Metrics export interval | 15 s | `internal/adapters/outbound/telemetry/telemetry.go` |
| Postgres pool | 10 connections, `statement_timeout = 5s` | `internal/adapters/outbound/postgres/pool.go` |
| Boot retry (migrations, first ping) | 5 attempts, backoff 1 s, 2 s, 4 s, 8 s (15 s in total) | `internal/bootretry/bootretry.go` |
| Outbox relay batch size | 100 rows per pass; a full batch is followed at once by another pass | `internal/adapters/outbound/outbox/relay.go` |
| Kafka producer | acks=all, hash balancer on the message key (the aggregate id), `AllowAutoTopicCreation` | `internal/adapters/outbound/kafka/writer.go` |
| Consumer retry backoff | 200 ms doubling to a 5 s cap, at most 5 handling attempts, then `<topic>.dlq` | `internal/adapters/inbound/kafka/kafka.go`, `deadletter.go` |
| Broker connection blips in a consumer | up to 10 consecutive transient fetch failures are slept off; the 11th ends the consumer and the process exits | `internal/adapters/inbound/kafka/kafka.go` (`maxTransientFetchFailures`) |
| `/readyz` verdict cache / check timeout | 3 s / 2 s | `internal/adapters/inbound/http/readiness.go` |
| Idempotency key retention | 24 h (older rows are ignored by the lookup; the code has no purge job) | `internal/adapters/outbound/postgres/idempotency.go`, table `idempotency_keys` |
| `Idempotency-Key` length / request body limit | 1 to 200 characters / 64 KiB | `internal/adapters/inbound/http/idempotency.go`, `response.go` (`maxBodyBytes`) |
| Topics produced | `retailnetwork.last-mile-delivery.events`, `retailnetwork.last-mile-delivery.analytics` | `internal/adapters/outbound/kafka/encoder.go` |
| Topics consumed | `retailnetwork.linehaul-transportation.events`, `retailnetwork.node-topology.events`, `retailnetwork.shipping-and-tracking.events` | `internal/adapters/inbound/kafka/consumer.go` |

## What is deliberately absent

- **No auth variables.** REST, MCP and A2A are unauthenticated; access control is the in-cluster boundary.
- **No `*_MODE` variables.** The consumers and the relay always run; the chart refuses any `*_MODE` in `extraEnv`.
- **No topic-name variables.** Topic names are constants.
- **Chart values.** `charts/last-mile-delivery` maps these variables onto the pods; see the [Runbook](/docs/operations/runbook).
