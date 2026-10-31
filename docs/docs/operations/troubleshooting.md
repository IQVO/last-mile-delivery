---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
sidebar_position: 4
---

# Troubleshooting

Symptom, cause, check, fix. Every message quoted below is a string in the code. For infrastructure incidents (Postgres or Kafka down, parked outbox rows, DLQ) the [Runbook](/docs/operations/runbook) has the long form; this page is the lookup table.

## The process will not start

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode` (`cmd/api`), `DATABASE_URL is required: this context runs on real Postgres` (`cmd/mcp`, `cmd/a2a`) | variable unset | `env \| grep DATABASE_URL` in the pod; the chart refuses to render without `database.url` or `database.existingSecret` | set it (Secret key `DATABASE_URL`) |
| `KAFKA_BROKERS is required: this context runs on real Kafka (outbox relay, consumers, /readyz)` | `cmd/api` only; unset or only commas and blanks | chart value `kafka.brokers` | set a comma-separated list, e.g. `localhost:9092` |
| `LINEHAUL_CONSUMER_GROUP is required: the consumer always runs on real Kafka` (same for `NODE_TOPOLOGY_CONSUMER_GROUP`, `SHIPPING_CONSUMER_GROUP`) | the group id is deliberately without default | `config.linehaulConsumerGroup`, `config.nodeTopologyConsumerGroup`, `config.shippingConsumerGroup` | set a stable, service-prefixed id (the fleet uses `last-mile-delivery-linehaul`, `-node-topology`, `-shipping`) |
| `A2A_AGENT_URL is required: the Agent Card must advertise the absolute http(s) URL ...` or `A2A_AGENT_URL="..." is not an absolute http(s) URL` | `cmd/a2a` has no default for the advertised URL | the value must have `http` or `https` and a host | e.g. `http://last-mile-delivery-a2a.warehouse-systems.svc.cluster.local:8093/` |
| `run migrations: ...` after retries, pod killed by the startup probe | wrong or unreachable `DATABASE_URL`/`MIGRATIONS_DATABASE_URL`, or a pooled DSN used for migrations | logs show `retrying` WARN lines with `op="run migrations"` (5 attempts, 15 s of backoff) | point `MIGRATIONS_DATABASE_URL` at a direct session-mode DSN; golang-migrate's advisory lock does not work through PgBouncer transaction pooling |
| `http server listening` never appears, `bind: address already in use` | `HTTP_ADDR`, `MCP_ADDR` or `A2A_ADDR` collides with another process | `lsof -i :8080` | change the address |
| Helm: `config.linehaulConsumerGroup is required ...`, `... sets MODE ...`, `terminationGracePeriodSeconds is 30 - want at least 55` | chart guard rails | the message names the value | set the value; there is no `*_MODE` to set |

## REST answers errors

Errors are RFC 9457 `application/problem+json`; the `type` field is the slug.

| Response | Cause | Fix |
| --- | --- | --- |
| `400 idempotency-key-required` | a state-changing `POST` without `Idempotency-Key`, or one longer than 200 characters | send a unique key per logical request |
| `422 idempotency-key-reused` | the same key and path with a different body | use a new key; to retry the same request, resend the identical body (the stored 2xx response is replayed with `Idempotent-Replayed: true`) |
| `422 unknown-station` | the station is not in `rm_stations` as a `DELIVERY_STATION` | check `SELECT * FROM rm_stations;`; the node-topology consumer fills it from `NodeRegistered`. Standalone, seed the row (see [Quickstart](/docs/overview/quickstart)) |
| `422 invalid-wave-times` | the times do not run `inductCutoffAt < windowStart < windowEnd` | fix the body |
| `422 invalid-van-capacity` | `vanCapacity` is zero or negative | send a positive capacity |
| `422 validation-failed` | malformed JSON, missing or malformed field, NUL byte in the query or body (a NUL in the path is `404`) | read `detail` |
| `404 not-found` / `404 package-not-on-route` | unknown wave, route or partner id / the tracking number is not on that route | list first with `GET /waves` |
| `409 wave-full`, `induction-closed`, `already-inducted` | the wave has no stop capacity left, induction is closed, or the package is already in it | schedule another wave, or wait for the next induction |
| `409 package-not-inducted`, `package-already-routed`, `stop-key-mismatch`, `route-over-capacity`, `partner-inactive`, `partner-station-mismatch` | `POST /routes` rules | check the wave's stops with `GET /waves/{waveId}/stops`, the partner's state and `vanCapacity` |
| `409 route-not-started`, `route-incomplete`, `attempt-already-recorded`, `already-delivered` | attempt and completion rules | start the route first; a package has at most one attempt per route and is delivered at most once across routes |
| `409 illegal-transition` | e.g. dispatching a cancelled wave | `GET /waves` for the current `state` |
| `409 concurrent-update` | another request changed the aggregate (version guard) | re-fetch and retry |
| `500 internal-error` | untyped failure; the real cause is logged server-side, not returned | find the log line by `request_id`; the business counter records it as `outcome=rejected, reason=internal-error` for attempts |
| `404 not-found` on an unknown path, `405 method-not-allowed` | wrong route; the colon verbs are `:close-induction`, `:dispatch`, `:cancel`, `:start`, `:deactivate` | see the [API reference](/docs/api-reference) |

## Events do not flow

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Writes succeed but downstream never sees the event | outbox row unpublished or parked | `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL ORDER BY id;` | Kafka reachable? If `attempts >= OUTBOX_MAX_ATTEMPTS` the row is parked: fix the cause and re-arm with `UPDATE outbox_events SET attempts = 0, last_error = NULL WHERE id = <id>` |
| `outbox relay pass failed` every second | broker down or unreachable `KAFKA_BROKERS` | `/readyz` says `not ready: kafka` | restore connectivity; events are delayed, not lost |
| First outbox attempt fails, second succeeds | the broker auto-creates a topic on first write | `attempts = 2` on the first row of a new topic | none; expected |
| No `consumer running` line for a consumer, or the process exits with `... stopped unexpectedly: exiting so the orchestrator restarts the service` | a consumer or the relay ended on its own (for example a reader error that is not a transient connection blip) | the ERROR line `consumer stopped` or `outbox relay stopped` | fix the broker problem; the orchestrator restarts the pod. Up to 10 consecutive transient fetch failures are tolerated |
| No waves can be scheduled and no routes planned after a fresh deployment | read models empty because the consumer group has no committed offset on a topic that already holds older events, or the producer is silent | `SELECT count(*) FROM rm_stations;`, `rm_station_service_areas`, `rm_package_stops`, `rm_inbound_packages` | a new group starts at the first available offset and replays the topic; if it is empty, the upstream context has produced nothing |
| Retries on one partition: `handling failed; retrying the same message` | transient failure (Postgres down, timeout) | the `error` field | restore the dependency; after 5 attempts the message goes to `<topic>.dlq` |
| A message sits in `<topic>.dlq` | undecodable CloudEvent, invalid payload, or 5 failed attempts | headers `x-dlq-error`, `x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset` | fix the cause, republish the DLQ message to the original topic; the inbox `processed_events` makes replays idempotent |
| Packages never reach a wave | `LoadArrived` for a node that is not a delivery station, or no wave with capacity before the induct cutoff | `package_histories.status`, `rm_inbound_packages`; a package with no eligible wave publishes `PackageInductionDeferred` | schedule a wave for that station and date |
| An unknown event type on a consumed topic | consumers dispatch on the full CloudEvents `type` and ignore types they have no handler for | nothing logged | none; not an error |

## Readiness and health

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `/readyz` `503 {"status":"not_ready"}` | the pod received SIGTERM and is draining | pod phase | none |
| `/readyz` `503 dependency-unavailable`, detail `not ready: postgres` / `not ready: kafka` | the ping or metadata request failed within 2 s | WARN log `readiness: dependency is not ready` with `dependency` and `error` | restore the dependency; the verdict is cached for 3 s |
| `/readyz` 200 but REST writes `500` | schema or permission problem after the ping | `internal-error` log lines with the SQL error | check the migrations ran (`schema_migrations` table) and the DB role's rights |
| `cmd/mcp` / `cmd/a2a` has no `/readyz` | by design; only `/healthz` exists | | probe `/healthz` |

## MCP and A2A

| Symptom | Cause | Fix |
| --- | --- | --- |
| MCP call returns `isError` text `not-found: ...` or `validation-failed: ...` | unknown id, or `serviceDate` not `YYYY-MM-DD` | pass the documented input; every field is required and unknown fields are refused |
| A2A answers JSON-RPC error `-32009` `VERSION_NOT_SUPPORTED` (`requested "0.3", served "1.0"`) | the client sent no `A2A-Version` header | send `A2A-Version: 1.0` |
| A2A returns an in-band problem (`not-found` 404, `validation-failed` 422, `internal-error` 500) | skill rejections are answered inside the message, not as JSON-RPC errors; request-shape problems are 400 | read the data part |
| Agent Card advertises an address the client cannot reach | `A2A_AGENT_URL` is the in-cluster DNS by default | set `a2a.agentUrl` to the Kong path when needed |
| MCP/A2A return empty lists after a restart | they read the service database `cmd/api` writes; a different `DATABASE_URL` shows a different (empty) schema | point them at the same database |

## Telemetry is missing

| Symptom | Cause | Fix |
| --- | --- | --- |
| No traces or metrics in Jaeger/Prometheus, service works | Collector unreachable; export never blocks and failures are logged at DEBUG only | run with `LOG_LEVEL=debug` and look for `opentelemetry error`; check `OTEL_EXPORTER_OTLP_ENDPOINT` |
| Logs lack `trace_id` | the record was written without a span-carrying context (a log outside a request or consumer) | none; expected for boot lines |
| Business panel for `last_mile_delivery_package_attempted_total` is empty | no attempt was reported yet | record an attempt; see [Observability](/docs/operations/observability) |

## Tests fail locally

| Symptom | Cause | Fix |
| --- | --- | --- |
| `make integration` fails to start containers | Docker not running (testcontainers) | start Docker |
| `golangci-lint` reports "parallel golangci-lint is running" on `git push` | the pre-push hook runs `make check` and another lint is active | wait about a minute and push again |
| An architecture test fails after adding an event | the catalogue is pinned (`TestEventCatalogueMatchesContract`) | update the domain event, `apis/asyncapi.yaml` and ADR 0004 in the same commit |
