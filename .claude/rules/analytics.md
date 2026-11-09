---
paths:
  - "cmd/lm-projector/**"
  - "cmd/lm-reports/**"
  - "analytics/**"
  - "internal/analytics/**"
  - "internal/adapters/outbound/analyticsstore/**"
  - "internal/adapters/inbound/kafka/analytics_*.go"
  - "internal/adapters/inbound/reportshttp/**"
---

# Analytics read side (projector + reports)

Record: `docs/adr/0012-analytics-read-side.md`. The read side consumes the facts this context already publishes; do not
change the encoder, the outbox or the topic contracts from here.

- Sources: ONLY `retailnetwork.last-mile-delivery.events` and `retailnetwork.last-mile-delivery.analytics` (one reader,
  one group; never a sibling's topic). Eight projected types, dispatched on the FULL CloudEvents `type`
  (`internal/adapters/inbound/kafka/analytics_decode.go`); PackageInductionDeferred is acknowledged silently; anything
  else (and non-CloudEvents) is skipped with a sampled WARN.
- `cmd/lm-projector` is the ONLY writer of the SEPARATE analytical database (`ANALYTICS_DATABASE_URL`, `KAFKA_BROKERS`,
  `ANALYTICS_CONSUMER_GROUP` default `last-mile-delivery-analytics`, `ADMIN_ADDR` `:8091`). It never opens the OLTP
  database. `cmd/lm-reports` (`HTTP_ADDR` `:8092`) reads through a read-only pool (`ANALYTICS_READER_DATABASE_URL`,
  falling back to `ANALYTICS_DATABASE_URL`).
- Dedupe on the CloudEvents `id` and the fact write are ONE analytical-database transaction (`Projection.Apply`); test
  atomicity with a trigger that RAISEs (`postgres_integration_test.go`). Every fact statement is order-independent
  (facts arrive on different partitions and topics).
- Delivery policy: dead-letter ONLY deterministic poison (bad payload, subject mismatch, missing field, outcome outside
  the vocabulary, Postgres class 22/23) to `retailnetwork.last-mile-delivery.analytics.dlq`; retry transient failures
  forever with capped backoff.
- `internal/analytics/report` is pure and imports no other internal package (arch test). SQL only counts and sums; the
  completion rule, the outcome vocabulary, the unknown station, means, rates, range rules and the freshness lag live
  there, in `make mutation` and in the coverage scope. One contract suite (`analyticsstore/contract_test.go`) runs
  against `MemoryStore` and Postgres: change both twins together.
- `analytics/migrations` is embedded (`analytics/migrations.go`), additive, with down files; never edit an applied
  migration, add the next number.
- Reports are unauthenticated, read-only GETs under `/reports/...` (plus `/healthz`, `/readyz`), camelCase, UTC days,
  `from`/`to` inclusive dates, `null` for an undefined rate. Any route change edits `apis/openapi.yaml` (tag `reports`)
  and regenerates the REST reference in the same commit.
- Chart: the `analytics:` block is opt-in; keep `tests/render_test.sh` and `tests/test_service_selectors.py` green.
