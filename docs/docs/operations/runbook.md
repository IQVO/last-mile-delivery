# Last-Mile Delivery runbook

The Last-Mile Delivery bounded context runs each delivery station of the retail-network DDD study: it schedules `DeliveryWave`s (induct cutoff, delivery window, stop capacity) that the delivery promise relies on, plans `Route`s that hand inducted packages to delivery service partners, and records every delivery attempt (`PackageAttemptHistory`; the third failure publishes `PackageReturnInitiated`, a package with no eligible wave publishes `PackageInductionDeferred`). It consumes linehaul, node-topology and shipping events to build its read models, and publishes its own integration events on Kafka via a transactional outbox. Source of truth: `README.md`, `docs/adr/`, `apis/openapi.yaml` and `apis/asyncapi.yaml`.

## Runtime shape

One `api` Deployment (`charts/last-mile-delivery/`, `replicaCount: 1`), one process `cmd/api` running four loops side by side (`cmd/api/main.go`):

- **REST** on `HTTP_ADDR` (default `:8080`): the 12 operations of `apis/openapi.yaml` plus `/healthz` and `/readyz` (`internal/adapters/inbound/http/server.go`). State-changing calls require an `Idempotency-Key` (replayed for 24 h, table `idempotency_keys`); errors are RFC 9457 problem+json. Surface: `POST /waves`, `GET /waves`, `GET /waves/{waveId}/stops`, `POST /waves/{waveId}:close-induction|:dispatch|:cancel`, `POST /routes`, `GET /routes/{routeId}`, `POST /routes/{routeId}:start`, `POST /routes/{routeId}/attempts`, `POST /partners`, `POST /partners/{dspId}:deactivate`.
- **Three Kafka consumers** (local copy), each under its own stable group id, feeding read models and the InductOnArrival policy (`internal/adapters/inbound/kafka/consumers.go`):

| Consumer | Topic | Event types handled |
| --- | --- | --- |
| linehaul | `retailnetwork.linehaul-transportation.events` | `LoadArrived` |
| node-topology | `retailnetwork.node-topology.events` | `NodeRegistered`, `ServiceAreaAssigned` |
| shipping | `retailnetwork.shipping-and-tracking.events` | `LabelIssued` |

- **Always-on transactional outbox relay** draining `outbox_events` rows to Kafka every interval (`cmd/api/main.go` `startOutboxRelay`); there is no log-only mode. Kafka is dialled lazily on the first send.

If any consumer or the relay stops, the process exits on purpose so the orchestrator restarts it (`cmd/api/main.go` `serveUntilSignal`). On SIGTERM the shutdown is sequential: `/readyz` flips not-ready **first**, waits `SHUTDOWN_DRAIN_DELAY` (default 5 s), shuts HTTP down (10 s), then the consumers (10 s each) and the relay; the chart enforces `terminationGracePeriodSeconds: 60` to cover that worst case (`charts/last-mile-delivery/values.yaml`).

## Dependencies

- **Postgres** — the only runtime store. `DATABASE_URL` is the pooled DSN (PgBouncer in front on this cluster). Migrations are embedded (`internal/adapters/outbound/postgres/migrations/`) and run at boot with golang-migrate against `MIGRATIONS_DATABASE_URL`, the direct (non-pooled, session-mode) DSN used only for that startup step; if unset it falls back to `DATABASE_URL` (`cmd/api/wiring.go`). Both keys come from the Secret `last-mile-delivery-db` (chart keys `database.existingSecretKey` = `DATABASE_URL` and `database.migrationsExistingSecretKey` = `MIGRATIONS_DATABASE_URL`, `charts/last-mile-delivery/values.yaml`).
- **Kafka** — `KAFKA_BROKERS`, comma-separated. Publishes `retailnetwork.last-mile-delivery.events` (`DeliveryWaveScheduled`, `DeliveryWaveCapacityChanged`, `DeliveryWaveCancelled`, `PackageOutForDelivery`, `DeliveryAttempted`, `PackageInductionDeferred`, `PackageReturnInitiated`) and `retailnetwork.last-mile-delivery.analytics` (`RoutePlanned`) — `internal/adapters/outbound/kafka/encoder.go`. All writers set `AllowAutoTopicCreation`, so topics are created by the first writer; there is no manual topic provisioning step.
- Env is validated **before anything is dialled**: a missing `DATABASE_URL`, `KAFKA_BROKERS` or consumer-group var exits at boot naming the missing one (`cmd/api/config.go`). The boot migration is wrapped in a bounded retry inside the ~60 s startup-probe window (startupProbe `/healthz`, `periodSeconds: 2` × `failureThreshold: 30`).

## Health

- `GET /healthz` — liveness only, returns `200 {"status":"ok"}`; it never checks dependencies and is never flipped by shutdown (`internal/adapters/inbound/http/readiness.go`). Chart probes: startup and liveness both use `/healthz` (`initialDelaySeconds: 5`, `periodSeconds: 10`).
- `GET /readyz` — the drain gate plus dependency checks: a Postgres ping and a Kafka broker check; each verdict is cached 3 s and one round is bounded to 2 s, so probes cannot hammer the dependencies. Responses: `200 {"status":"ready"}`; `503 {"status":"not_ready"}` once draining; `503 problem+json` slug `dependency-unavailable` naming the failing dependency (`not ready: kafka`, `not ready: postgres`). The readinessProbe polls it every 5 s.

Whether writes are still accepted with a dependency down (per `cmd/api/wiring_integration_test.go`, which stops Kafka against the real process and keeps driving REST):

- **Kafka down** → `/readyz` 503 naming kafka, `/healthz` 200. REST writes are **still accepted and queued**: the outbox row is committed in the same transaction as the aggregate, and the relay retries the publish every interval. Events are delayed, not lost.
- **Postgres down** → `/readyz` 503 naming postgres, `/healthz` 200. REST writes **fail** (Postgres is the store); nothing is queued anywhere.

## Configuration

Every env var `cmd/api` reads, and the chart value that sets it (`charts/last-mile-delivery/values.yaml`; each `config.*` key maps 1:1 to one env var, `database.*`/`kafka.*` to the Secret and brokers):

| Env var | Chart value | Default / notes |
| --- | --- | --- |
| `DATABASE_URL` | `database.url` or `database.existingSecret` (key `DATABASE_URL`) | Required; pooled DSN; boot exits without it. |
| `MIGRATIONS_DATABASE_URL` | `database.migrationsExistingSecretKey` | Optional; direct DSN for the startup migration only; falls back to `DATABASE_URL`. |
| `KAFKA_BROKERS` | `kafka.brokers` | Required; comma-separated. |
| `LINEHAUL_CONSUMER_GROUP` | `config.linehaulConsumerGroup` | Required; stable group id (linehaul `LoadArrived`). |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | `config.nodeTopologyConsumerGroup` | Required; stable group id (`NodeRegistered`, `ServiceAreaAssigned`). |
| `SHIPPING_CONSUMER_GROUP` | `config.shippingConsumerGroup` | Required; stable group id (`LabelIssued`). |
| `HTTP_ADDR` | `config.httpAddr` | `:8080`. |
| `LOG_LEVEL` | `config.logLevel` | `info`; also `debug` / `warn` (`warning`) / `error`. JSON slog on stdout. |
| `OUTBOX_RELAY_INTERVAL` | `config.outboxRelayInterval` | Binary default 1 s; Go duration (`500ms`, `2s`) or plain seconds; invalid falls back with a WARN. |
| `OUTBOX_MAX_ATTEMPTS` | `config.outboxMaxAttempts` | Default 20; `0` = unlimited; a typo falls back to the default, never removes the cap. |
| `SHUTDOWN_DRAIN_DELAY` | `config.shutdownDrainDelay` | `5s`; chart refuses a grace period below this + 50 s. |

## Alerts and dashboards

- Grafana dashboard uid `warehouse-last-mile-delivery` (namespace `warehouse-systems`).
- Pod-restart alerts exist for this service; there are **no business-metric alerts yet** — the service instruments no business metrics today, so any such dashboard panels stay empty until the service exports them. Do not read an empty business panel as an outage.
- Logs: Grafana Loki, label `app=last-mile-delivery` (JSON slog on stdout).

## Failure modes

**Pod crash-loops.**
Symptom: restarts, `CrashLoopBackOff`. Diagnosis: `kubectl -n warehouse-systems logs deploy/last-mile-delivery --previous`. Most causes exit at boot naming the missing env var (`cmd/api/config.go`) or hang in the migration retry until the startup probe kills the pod (Postgres or migrations DSN wrong, PgBouncer pool exhausted). Action: fix the Secret or chart value, then `rollout restart`.

**Postgres unreachable.**
Symptom: `/readyz` 503 `not ready: postgres`, `/healthz` 200, REST writes failing with 5xx. Diagnosis: check Postgres/PgBouncer from inside the cluster; `kubectl -n warehouse-systems exec deploy/last-mile-delivery -- wget -qO- localhost:8080/readyz`. Action: restore Postgres; the pod recovers by itself (the readiness verdict recaches within 3 s). Nothing is queued during the outage — writes failed, clients must retry (idempotency keys make that safe).

**Kafka unreachable.**
Symptom: `/readyz` 503 `not ready: kafka`, `/healthz` 200, REST writes **still accepted**; the relay logs publish failures each interval; consumers retry their fetches. Diagnosis: broker reachability from the pod; unpublished-row count climbing. Action: restore Kafka; the relay drains the `outbox_events` backlog in id order — verify with `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;` falling back to 0.

**Outbox relay stuck / row parked after max attempts.**
Symptom: the unpublished count stops falling or freezes at a constant row; log line `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` (`internal/adapters/outbound/outbox/relay.go`, `AttemptCapReachedMsg`). A row that failed `OUTBOX_MAX_ATTEMPTS` (default 20) times is excluded from future claims and stays parked. Diagnosis: read the parked row's `last_error`. Action: fix the cause (usually Kafka reachability or an oversized message), then re-arm:
```sql
UPDATE outbox_events SET attempts = 0, last_error = NULL
WHERE id = <id> AND published_at IS NULL;
```
Columns per `internal/adapters/outbound/postgres/migrations/0001_last_mile_delivery_schema.up.sql` (`outbox_events`: `published_at`, `attempts`, `last_error`; unique `(event_id, topic)`; partial index `idx_outbox_events_unpublished` on rows `WHERE published_at IS NULL`).

**Consumer lag or retry logs.**
Symptom: repeated ERROR logs `... handling failed; retrying the same message` or `... offset commit failed; retrying the same message` (`internal/adapters/inbound/kafka/kafka.go`); a partition makes no progress. Note: a consumed topic whose producer has not written yet is **normal** and only logs retries — topics are auto-created by the first writer, so an empty or missing consumed topic is not an outage. A message whose handler keeps failing is retried with capped exponential backoff up to 5 attempts (`domainMaxHandlerAttempts`), then dead-lettered to `<topic>.dlq`; poison messages (not a CloudEvent, undecodable payload) are dead-lettered at once without retry. Diagnosis: read the `error` field of the retry log; inspect the `<topic>.dlq` topic for dead-lettered messages. Action: transient (a dependency down) → restore it; deterministic → fix the producer or the payload, then replay the DLQ message onto the original topic. Replay is safe: `processed_events` claims the CloudEvents id in the same transaction as the effect, so an already-processed id is a no-op.

## Operational actions

```sh
kubectl -n warehouse-systems rollout restart deploy/last-mile-delivery
kubectl -n warehouse-systems rollout status  deploy/last-mile-delivery
kubectl -n warehouse-systems logs deploy/last-mile-delivery --tail=200   # or Grafana Loki, app=last-mile-delivery
```

The service is reachable through the gateway route `/api/last-mile-delivery` (prefix stripped before it reaches the pod). Restarting is always safe: REST writes are idempotent (`Idempotency-Key`), consumers resume from committed offsets under their stable group ids, and unpublished events wait in the outbox.
