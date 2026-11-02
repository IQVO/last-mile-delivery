---
id: runbook
title: Runbook
sidebar_label: Runbook
sidebar_position: 2
---

# Last-Mile Delivery runbook

The Last-Mile Delivery bounded context runs each delivery station of the retail-network DDD study: it schedules `DeliveryWave`s (induct cutoff, delivery window, stop capacity) that the delivery promise relies on, plans `Route`s that hand inducted packages to delivery service partners, and records every delivery attempt (`PackageAttemptHistory`; the third failure publishes `PackageReturnInitiated`, a package with no eligible wave publishes `PackageInductionDeferred`). It consumes linehaul, node-topology and shipping events to build its read models, and publishes its own integration events on Kafka via a transactional outbox. Source of truth: the code, [`apis/openapi.yaml`](https://github.com/IQVO/last-mile-delivery/blob/develop/apis/openapi.yaml), [`apis/asyncapi.yaml`](https://github.com/IQVO/last-mile-delivery/blob/develop/apis/asyncapi.yaml) and the [ADRs](/docs/adr).

## Runtime shape

Up to three Deployments of one Helm chart (`charts/last-mile-delivery/`), all reading and (for `api`) writing the same service database:

| Component | Binary | Chart switch | Port | Role |
| --- | --- | --- | --- | --- |
| `api` | `cmd/api` | always on (`replicaCount: 1`) | `8080` | REST, three Kafka consumers and the always-on outbox relay in one process |
| `mcp` | `cmd/mcp` | `mcp.enabled` (default off) | `8090` | read-only MCP server, `/mcp` and `/`, `/healthz` |
| `a2a` | `cmd/a2a` | `a2a.enabled` (default off) | `8093` | read-only A2A agent, JSON-RPC on `/` and `/a2a`, Agent Card, `/healthz` |
| `frontend` | `web/` | `frontend.enabled` (default off) | `8080` (Service `80`) | operator micro-frontend (own nginx pod, ClusterIP only) |

`cmd/api` (`cmd/api/main.go`) runs four loops side by side:

- **REST** on `HTTP_ADDR` (default `:8080`): the 12 operations of `apis/openapi.yaml` plus `/healthz` and `/readyz` (`internal/adapters/inbound/http/server.go`). State-changing calls require an `Idempotency-Key` (1 to 128 printable ASCII characters, global, replayed for 24 h from table `idempotency_keys`; a replay carries `Idempotency-Replayed: true`, another request under the key is 409 `idempotency-key-reused`); errors are RFC 9457 problem+json; request bodies are capped at 64 KiB. Surface: `POST /waves`, `GET /waves`, `GET /waves/{waveId}/stops` (both paged: `limit`, `cursor`, `{items, nextCursor}`), `POST /waves/{waveId}/induction-closures`, `POST /waves/{waveId}/dispatches`, `POST /waves/{waveId}/cancellations`, `POST /routes`, `GET /routes/{routeId}`, `POST /routes/{routeId}/starts`, `POST /routes/{routeId}/attempts`, `POST /partners`, `POST /partners/{dspId}/deactivations`.
- **Three Kafka consumers** (local copies), each under its own stable group id (`internal/adapters/inbound/kafka/consumers.go`):

| Consumer | Topic | Group id variable | Event types handled |
| --- | --- | --- | --- |
| linehaul | `retailnetwork.linehaul-transportation.events` | `LINEHAUL_CONSUMER_GROUP` | `LoadArrived` |
| node-topology | `retailnetwork.node-topology.events` | `NODE_TOPOLOGY_CONSUMER_GROUP` | `NodeRegistered`, `ServiceAreaAssigned` |
| shipping | `retailnetwork.shipping-and-tracking.events` | `SHIPPING_CONSUMER_GROUP` | `LabelIssued` |

- **Always-on transactional outbox relay** draining `outbox_events` rows to Kafka (`startOutboxRelay`); there is no log-only mode. Kafka is dialled lazily on the first send.

If any consumer or the relay stops, the process exits non-zero on purpose so the orchestrator restarts it (`serveUntilSignal`). On SIGTERM the shutdown is sequential: `/readyz` flips to not-ready **first**, waits `SHUTDOWN_DRAIN_DELAY` (default 5 s), shuts HTTP down (10 s), then each consumer (10 s each) and the relay (10 s). The chart enforces `terminationGracePeriodSeconds: 60`, and refuses a value below `config.shutdownDrainDelay` + 50 s.

## Deployment (Helm chart)

```sh
helm template r charts/last-mile-delivery -f charts/last-mile-delivery/ci/default-values.yaml
```

The chart renders exactly the environment `cmd/api` reads (each `config.*` key maps 1:1 to one variable) and **refuses to render** without `database.url` or `database.existingSecret`, `kafka.brokers`, and the three stable consumer groups `config.linehaulConsumerGroup`, `config.nodeTopologyConsumerGroup`, `config.shippingConsumerGroup`; it also refuses any `*_MODE` in `extraEnv`, any `extraEnv` that duplicates an observability variable the chart renders, and a grace period below the shutdown bounds. The fleet values live in `warehouse-infra` (`helm-values/last-mile-delivery.yaml`): groups `last-mile-delivery-linehaul`, `last-mile-delivery-node-topology`, `last-mile-delivery-shipping`, DB Secret `last-mile-delivery-db`, Kong path `/api/last-mile-delivery` (prefix stripped), MCP at `/api/mcp/last-mile-delivery`.

| Chart value | Env var | Notes |
| --- | --- | --- |
| `database.url` / `database.existingSecret` + `existingSecretKey` | `DATABASE_URL` | always from a Secret (`secretKeyRef`), never the ConfigMap |
| `database.migrationsExistingSecretKey` | `MIGRATIONS_DATABASE_URL` | optional key in the same Secret (`optional: true`) |
| `kafka.brokers` | `KAFKA_BROKERS` | comma-separated, required |
| `config.linehaulConsumerGroup`, `config.nodeTopologyConsumerGroup`, `config.shippingConsumerGroup` | the three `*_CONSUMER_GROUP` | required, service-prefixed, never the pod name |
| `config.httpAddr`, `config.logLevel`, `config.outboxRelayInterval`, `config.outboxMaxAttempts`, `config.shutdownDrainDelay` | `HTTP_ADDR`, `LOG_LEVEL`, `OUTBOX_RELAY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS`, `SHUTDOWN_DRAIN_DELAY` | |
| `otel.enabled`, `otel.endpoint`, `otel.serviceName`, `environment` | `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME` (`-mcp`/`-a2a` suffix for those components), `ENVIRONMENT` | `SERVICE_VERSION` is the image tag |
| `mcp.httpAddr`, `mcp.serviceVersion`, `mcp.environment`, `mcp.route.*` | `MCP_ADDR`, `SERVICE_VERSION`, `ENVIRONMENT` | route off by default |
| `a2a.httpAddr`, `a2a.agentUrl`, `a2a.serviceVersion`, `a2a.environment`, `a2a.route.*` | `A2A_ADDR`, `A2A_AGENT_URL`, ... | empty `agentUrl` = the release's in-cluster Service DNS `http://<release>-a2a.<ns>.svc.cluster.local:8093/` |
| `autoscaling.api.*` | | HPA off by default; N>1 is designed for (relay claims rows with `FOR UPDATE SKIP LOCKED`, writes are version-guarded, consumers share one group and dedupe on the CloudEvents id) but has not been load-tested |

The pod runs non-root (uid 1000) with a read-only root filesystem, no privilege escalation, all capabilities dropped and the `RuntimeDefault` seccomp profile. Probes: `startupProbe` and `livenessProbe` on `/healthz` (startup `periodSeconds: 2` x `failureThreshold: 30` = about 60 s; liveness `initialDelaySeconds: 5`, `periodSeconds: 10`), `readinessProbe` on `/readyz` (`initialDelaySeconds: 3`, `periodSeconds: 5`). Chart tests: `bash charts/last-mile-delivery/tests/render_test.sh` and `python3 charts/last-mile-delivery/tests/test_service_selectors.py` (CI job `chart-tests`). The release job packages `last-mile-delivery-<version>.tgz` to `oci://ghcr.io/iqvo`; what must be true before the first release is in [Before the first release](/docs/operations/before-first-release).

## Dependencies

- **Postgres**: the only runtime store. `DATABASE_URL` is the pooled DSN (PgBouncer in front on this cluster). Migrations are embedded (`internal/adapters/outbound/postgres/migrations/`, one file pair `0001_last_mile_delivery_schema`) and run at boot with golang-migrate against `MIGRATIONS_DATABASE_URL`, the direct (non-pooled, session-mode) DSN used only for that step; unset falls back to `DATABASE_URL`. `cmd/mcp` and `cmd/a2a` run the same idempotent migrations at their own boot (golang-migrate's advisory lock makes concurrent starts safe), so any of the three can create the schema. To roll back by hand use the `.down.sql` next to the `.up.sql`; there is no automatic down-migration.
- **Kafka**: `KAFKA_BROKERS`, comma-separated; one broker platform-wide. Produced: `retailnetwork.last-mile-delivery.events` (`DeliveryWaveScheduled`, `DeliveryWaveCapacityChanged`, `DeliveryWaveCancelled`, `PackageOutForDelivery`, `DeliveryAttempted`, `PackageInductionDeferred`, `PackageReturnInitiated`) and `retailnetwork.last-mile-delivery.analytics` (`RoutePlanned`, analytics-only), both in `internal/adapters/outbound/kafka/encoder.go`. Consumed: the three topics above, each with a dead-letter topic `<topic>.dlq`. All writers set `AllowAutoTopicCreation`, so topics are created by the first writer; there is no manual topic provisioning step.
- Env is validated **before anything is dialled**: a missing `DATABASE_URL`, `KAFKA_BROKERS` or consumer-group variable exits at boot naming it (`cmd/api/config.go`). The boot migration and the first Postgres ping are wrapped in a bounded retry (5 attempts, 1 s doubling, 15 s of backoff in total, `internal/bootretry`) because the first outbound dial of an Istio-injected pod can be reset; it fits inside the startup probe window.

## Health

- `GET /healthz`: liveness only, `200 {"status":"ok"}`; never checks dependencies and is never flipped by shutdown. All three binaries serve it.
- `GET /readyz` (`cmd/api` only): the drain gate plus a Postgres ping and a Kafka broker metadata request on a fresh connection (topic list empty, so reachability only); each verdict is cached 3 s and one round is bounded to 2 s. Responses: `200 {"status":"ready"}`; `503 {"status":"not_ready"}` once draining; `503 problem+json` slug `dependency-unavailable` naming the failing dependency (`not ready: kafka`, `not ready: postgres`).

Whether writes are still accepted with a dependency down (proved by `cmd/api/wiring_integration_test.go`, which stops Kafka against the real process and keeps driving REST):

- **Kafka down**: `/readyz` 503 naming kafka, `/healthz` 200. REST writes are **still accepted and queued**: the outbox row is committed in the same transaction as the aggregate and the relay retries every interval. Events are delayed, not lost.
- **Postgres down**: `/readyz` 503 naming postgres, `/healthz` 200. REST writes **fail**; nothing is queued anywhere.

## Configuration

See [Configuration](/docs/operations/configuration) for every variable of the three binaries.

## Alerts and dashboards

- Grafana dashboard uid `warehouse-last-mile-delivery` (namespace `warehouse-systems`), including the business panel for `last_mile_delivery_package_attempted_total`.
- Alerts in `warehouse-infra`: Kong 5xx ratio, pod restarting (at least 2 restarts in 15 m), and the availability and latency burn-rate alerts. There is no alert on outbox backlog or consumer lag. Details: [Observability](/docs/operations/observability).
- Logs: Grafana Loki, label `app=last-mile-delivery` (JSON slog on stdout).

## Failure modes

**Pod crash-loops.**
Symptom: restarts, `CrashLoopBackOff`. Diagnosis: `kubectl -n warehouse-systems logs deploy/last-mile-delivery --previous`. Most causes exit at boot naming the missing variable (`cmd/api/config.go`) or hang in the migration retry until the startup probe kills the pod (Postgres or migrations DSN wrong, PgBouncer pool exhausted). Also: a consumer or the relay that ends on its own makes the process exit by design. Action: fix the Secret or chart value, then `rollout restart`.

**Postgres unreachable.**
Symptom: `/readyz` 503 `not ready: postgres`, `/healthz` 200, REST writes failing with 5xx. Diagnosis: check Postgres/PgBouncer from inside the cluster; `kubectl -n warehouse-systems exec deploy/last-mile-delivery -- wget -qO- localhost:8080/readyz`. Action: restore Postgres; the pod recovers by itself (the verdict recaches within 3 s). Nothing is queued during the outage; clients must retry (idempotency keys make that safe).

**Kafka unreachable.**
Symptom: `/readyz` 503 `not ready: kafka`, `/healthz` 200, REST writes **still accepted**; the relay logs `outbox relay pass failed` each interval; consumers retry their fetches. Diagnosis: broker reachability from the pod; unpublished-row count climbing. Action: restore Kafka; the relay drains the backlog in id order. Verify with `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;` falling back to 0.

**Outbox relay stuck / row parked after max attempts.**
Symptom: the unpublished count stops falling or freezes at a constant row; log line `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` (`internal/adapters/outbound/outbox/relay.go`, `AttemptCapReachedMsg`). A row that failed `OUTBOX_MAX_ATTEMPTS` (default 20) times is excluded from future claims and stays parked; later rows, even with the same key, overtake it. Diagnosis: read the parked row's `last_error`. Action: fix the cause (usually Kafka reachability or an oversized message), then re-arm:

```sql
UPDATE outbox_events SET attempts = 0, last_error = NULL
WHERE id = <id> AND published_at IS NULL;
```

Columns per the migration `0001_last_mile_delivery_schema.up.sql`: `outbox_events` has `published_at`, `attempts`, `last_error`, unique `(event_id, topic)`, and the partial index `idx_outbox_events_unpublished` on rows `WHERE published_at IS NULL`.

**Consumer lag, retry logs, DLQ.**
Symptom: repeated ERROR logs `... handling failed; retrying the same message` or `... offset commit failed; retrying the same message` (`internal/adapters/inbound/kafka/kafka.go`); a partition makes no progress. A consumed topic whose producer has not written yet is **normal**: topics are auto-created by the first writer. A message whose handler keeps failing with a transient error is retried with capped exponential backoff (200 ms doubling to 5 s) up to 5 attempts (`domainMaxHandlerAttempts`), then published to `<topic>.dlq`; a message that can never be applied (not a CloudEvent, undecodable payload, invalid values) is dead-lettered at once without retry. The DLQ copy keeps the original key, value and headers and adds `x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset`, `x-dlq-error` and `x-dlq-failed-at`. A DLQ publish is retried forever, so a partition can still stall if the broker refuses the DLQ write. Diagnosis: read the `error` field of the retry log; inspect the `<topic>.dlq` topic. Action: transient (a dependency down) means restore it; deterministic means fix the producer or the payload, then replay the DLQ message onto the original topic. Replay is safe: `processed_events` claims the CloudEvents id in the same transaction as the effect, so an already-processed id is a no-op.

**Wave or partner writes answer `422 unknown-station`.**
The station read model `rm_stations` is fed by `NodeRegistered` events with `nodeType` `DELIVERY_STATION`. If node-topology's events never arrived (consumer group on a new empty topic offset, node-topology down), no wave can be scheduled. Check the node-topology consumer logs and `SELECT * FROM rm_stations;`.

## Operational actions

```sh
kubectl -n warehouse-systems rollout restart deploy/last-mile-delivery
kubectl -n warehouse-systems rollout status  deploy/last-mile-delivery
kubectl -n warehouse-systems logs deploy/last-mile-delivery --tail=200   # or Grafana Loki, app=last-mile-delivery
```

The API is reachable through the gateway route `/api/last-mile-delivery` (prefix stripped before it reaches the pod). Restarting is always safe: REST writes are idempotent (`Idempotency-Key`), consumers resume from committed offsets under their stable group ids, and unpublished events wait in the outbox. The MCP and A2A pods are stateless readers: restart them freely (`deploy/last-mile-delivery-mcp`, `deploy/last-mile-delivery-a2a`).
