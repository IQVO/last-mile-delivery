# ADR 0005: runtime, outbox, HTTP and readiness

## Status

Accepted (2026-10-23). The REST idempotency and list details below (per-route key, 422 on reuse, `Idempotent-Replayed`, 200-character keys, unpaged lists) are superseded by [ADR 0010](./0010-adopt-the-fleet-rest-api-guideline.md).

## Context

ADR 0002 to 0004 fix the domain, the consumed contracts and the event envelope. This ADR records how the
service runs: the process, the outbox relay, the HTTP edge and the readiness gate.

## Decision

- **Real runtime only.** `cmd/api` wires Postgres and Kafka. `DATABASE_URL`, `KAFKA_BROKERS` and the three
  consumer group variables (`LINEHAUL_CONSUMER_GROUP`, `NODE_TOPOLOGY_CONSUMER_GROUP`, `SHIPPING_CONSUMER_GROUP`)
  are mandatory; the boot error names the missing variable and is raised before the listener binds. The memory
  adapters are test doubles and `cmd/**` never imports them.
- **Outbox.** A use case saves the aggregates and appends the encoded events in ONE Postgres transaction. The relay
  is always on (`OUTBOX_RELAY_INTERVAL` default 1 s; `OUTBOX_MAX_ATTEMPTS` default 20, 0 = unlimited): delivery is
  at-least-once and a row that can never be sent stops after the cap instead of blocking the relay.
- **Worker death.** When the relay or a consumer ends on its own, the process exits with an error naming it, so the
  orchestrator restarts it. A broker outage therefore turns `/readyz` to 503 first (the gate caches a verdict for 3 s)
  and then ends the process once the consumers give up.
- **HTTP.** chi router implementing every operation of `apis/openapi.yaml`. Every state-changing POST requires
  `Idempotency-Key`: table `idempotency_keys`, key = (route, key), 24 h, the first response is replayed, the same key
  with another request is 422 `idempotency-key-reused`. A NUL byte is 404 `resource-not-found` in a path and 422 `validation-failed` in a query or body. Errors are
  RFC 9457 problem+json whose `type` is the slug: `not-found`, `illegal-transition`, `concurrent-update`,
  `validation-failed` and one kebab-case slug per model `Err` name (`internal/adapters/inbound/http/errors.go`).
- **Readiness.** `/healthz` is liveness and asks no dependency. `/readyz` is the drain gate plus a Postgres ping plus a
  Kafka metadata request with an EMPTY, non-nil topic list on a FRESH connection per check (the client's metadata call
  is served from a cached layout and would stay ready after the broker is gone). A failing dependency is 503
  `dependency-unavailable` naming it.
- **Shutdown.** `/readyz` 503, `SHUTDOWN_DRAIN_DELAY`, drain HTTP, stop consumers and relay, close the pool.

## Consequences

- The real-wiring test (`cmd/api/wiring_integration_test.go`) stops the broker and asserts the 503, the 200 on
  `/healthz`, an accepted write (the outbox holds it) and the exit of the service.
- Helm chart, Secrets and pod hardening are out of scope here (`docs/before-first-release.md`, rows 5 to 9).
