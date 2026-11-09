# ADR 0012: analytics read side

## Status

Accepted (2026-11-09). Additive: no published payload, topic, OLTP table or REST route of the api changes. Adds two
binaries (`cmd/lm-projector`, `cmd/lm-reports`), one analytical-database migration set (`analytics/migrations`), four
read-only report routes, two default-off chart components and a second, separate database (roadmap F9).

## Context

Last-Mile Delivery owns the waves of every delivery station, the routes of the delivery service partners and every
delivery attempt (ADR 0002). That makes it the only context that can answer three station questions as a trend: how
efficient the routes are (stops per route, packages per stop, the declared cost per stop and per package), how often
the first delivery attempt succeeds, and how much of the scheduled wave capacity the inductions use. Nothing queries
that today: the facts leave as CloudEvents and the OLTP tables hold the current state only.

shipping-and-tracking ADR 0012 is the pilot of the retail-network analytics read side (a separate analytical Postgres
fed by a projector, a read-only reports binary over it); this ADR copies its shape. The difference here: this context
publishes on BOTH of its own topics (ADR 0004). Seven of the eight integration events are on
`retailnetwork.last-mile-delivery.events` together with `WaveCostDeclared` (ADR 0011), and `RoutePlanned`, which
carries the stop count of a route, is on `retailnetwork.last-mile-delivery.analytics` ONLY. Every fact the reports need
is already on one of those two topics.

## Decision

### 1. What is projected

The projector consumes ONLY this context's own two topics, `retailnetwork.last-mile-delivery.events` and
`retailnetwork.last-mile-delivery.analytics`, with one reader in one consumer group. It never reads a sibling's topic
(not shipping-and-tracking's journey, not linehaul-transportation's arrivals, not node-topology's stations). No event is
fanned out (`FanoutEncoder` is not needed: `RoutePlanned` is already on the analytics topic) and the outbox is
unchanged.

Eight types are projected, dispatched on the FULL CloudEvents `type`:

| type | topic | payload fields read | fact |
| --- | --- | --- | --- |
| `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled` | events | `waveId` (= subject), `stationNodeId`, `serviceDate`, `stopCapacity` | `waves` (one row per wave) |
| `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCapacityChanged` | events | `waveId` (= subject), `assignedStops` | `wave_inductions` (the highest `assignedStops` per wave) |
| `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCancelled` | events | `waveId` (= subject) | `wave_cancellations` (one row per wave) |
| `com.retailnetwork.logistics.last-mile-delivery.wave.WaveCostDeclared` | events | `waveId` (= subject), `costPerStopCents`, `declaredAt` | `wave_costs` (the latest declaration per wave) |
| `com.retailnetwork.logistics.last-mile-delivery.route.RoutePlanned` | analytics | `routeId` (= subject), `waveId`, `dspId`, `stopCount` | `route_plans` (one row per route) |
| `com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery` | events | `routeId` (= subject), `trackingNumber`, `stationNodeId`, `at` | `route_packages` (one row per route and package) |
| `com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted` | events | `routeId` (= subject), `trackingNumber`, `outcome`, `at` | `delivery_attempts` (one row per attempt) |
| `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated` | events | `trackingNumber` (= subject) | `package_returns` (one row per event) |

The one other published type, `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageInductionDeferred`,
is acknowledged and not projected: no report needs it.

Tables of the analytical database (`analytics/migrations/0001_last_mile_facts`):

- `analytics_processed_events (event_id PK, event_type, occurred_at, applied_at)`: the dedupe set; `occurred_at` is the
  CloudEvents `time`, the basis of the freshness lag.
- `waves (wave_id PK, station_node_id, service_date, stop_capacity, event_id)`.
- `wave_inductions (wave_id PK, assigned_stops, event_id)`: `assignedStops` only grows (one event per NEW stop, ADR
  0002), so the row keeps the highest value seen and a redelivered or reordered event cannot lower it.
- `wave_cancellations (wave_id PK, cancelled_at, event_id)`.
- `wave_costs (wave_id PK, cost_per_stop_cents, declared_at, event_id)`: the last declaration wins (ADR 0011), by
  `declaredAt`, then by event id.
- `route_plans (route_id PK, wave_id, dsp_id, stop_count, event_id)`.
- `route_packages (route_id, tracking_number, station_node_id, out_at, event_id; PK (route_id, tracking_number))`.
- `delivery_attempts (event_id PK, route_id, tracking_number, outcome IN (DELIVERED, FAILED), attempted_at)`.
- `package_returns (event_id PK, tracking_number, initiated_at)`.

Wave events are keyed by `waveId`, route events by `routeId`, history events by `trackingNumber`, and `RoutePlanned`
travels on another topic: they sit on different partitions and may arrive in any order. The model is therefore
order-independent: it stores facts and the reports join them at query time. A fact whose station is not (yet) known
reports under the station `UNKNOWN`.

### 2. KPI definitions

Days are UTC calendar days of the **business instant in the payload** where there is one (`at` of an attempt, the
wave's `serviceDate`); `PackageReturnInitiated` carries no instant, so its day is its CloudEvents `time` (the domain
occurred-at of the third failed attempt that raised it). The station is the delivery station node id
(`stationNodeId`). Rates and means are `null` when their denominator is zero; counts are integers.

**Completed route.** `CompleteRoute` raises no event (ADR 0002, G34), so the projection cannot see the `COMPLETED`
transition. A route counts as completed when the model holds its `RoutePlanned`, at least one `PackageOutForDelivery`
and an attempt for EVERY one of its out-for-delivery packages: that is exactly the precondition `CompleteRoute` enforces
(`ErrRouteIncomplete`). Its day is the UTC day of its LAST attempt; its station is the `stationNodeId` of its
out-for-delivery packages. On a completed route every package was attempted once and, since every stop holds at least
one package, every stop was attempted: so its packages attempted = its out-for-delivery packages and its distinct stops
attempted = its `stopCount`.

`GET /reports/route-efficiency?from=&to=`, per day and station:

- `routesCompleted` = completed routes;
- `stopsPerRoute` = (sum of their `stopCount`) / `routesCompleted`;
- `packagesPerStop` = (packages attempted on them) / (distinct stops attempted on them, = sum of `stopCount`): the
  consolidation measure;
- `declaredCostPerStopCents` = the stop-weighted mean of the waves' declared `costPerStopCents` = (sum over the completed
  routes whose wave declared a cost of `costPerStopCents` x `stopCount`) / (sum of `stopCount` of those routes);
  `null` when no wave of the day's completed routes declared a cost (0 is a declared cost, ADR 0011);
- `costPerPackageCents` = `declaredCostPerStopCents` x (stops of the costed routes) / (packages of the costed routes) =
  (sum of `costPerStopCents` x `stopCount`) / (sum of packages) over the same costed routes; `null` when undefined.

**Attempt.** One `DeliveryAttempted`; its outcome is one of the as-built vocabulary `DELIVERED`, `FAILED`
(`internal/domain/route`). Its station is the station of its route's out-for-delivery fact for that package.

`GET /reports/attempt-outcomes?from=&to=`, per day and station:

- `attempts`, `deliveredAttempts`, `failedAttempts` = the attempts whose `at` falls on the day, in total and by outcome;
- `packagesAttempted` = the packages whose FIRST attempt (earliest `at`, then event id, across all their routes) falls on
  the day, at that attempt's station;
- `packagesWithFirstAttemptSuccess` = those of them whose first attempt was `DELIVERED`;
- `firstAttemptSuccessRate` = `packagesWithFirstAttemptSuccess` / `packagesAttempted`;
- `meanAttemptsPerPackage` = (all attempts of those packages, on any day) / `packagesAttempted`;
- `returnsInitiated` = the `PackageReturnInitiated` events of the day, at the station of the package's latest attempt
  (`UNKNOWN` when none is projected).

**Wave.** One `DeliveryWaveScheduled`, bucketed by its `serviceDate` and its `stationNodeId`.

`GET /reports/wave-utilisation?from=&to=`, per service date and station:

- `wavesScheduled` = the waves of the service date; `wavesCancelled` = those of them with a `DeliveryWaveCancelled`;
- `stopCapacity` = the sum of their scheduled `stopCapacity`;
- `stopsInducted` = the sum of their inducted stops (the highest `assignedStops` of `DeliveryWaveCapacityChanged`, 0
  for a wave without one): the as-built induction fact;
- `utilisation` = `stopsInducted` / `stopCapacity`.

`GET /reports/freshness`: `asOf` = the newest CloudEvents `time` the projection applied, `lagSeconds` = now - `asOf`
(never negative); both `null` until the first event.

Range rules: `from` and `to` are UTC dates (`YYYY-MM-DD`), both inclusive; both omitted = the 30 days ending today;
only `to` = the 30 days ending on `to`; only `from` = `from` .. today; `from` after `to` or a range over 366 days is a
400 problem `invalid-report-range`. Empty results are empty arrays. Wire fields are camelCase (ADR 0010); the routes
are collection-shaped reports, `x-pagination-exempt: report`.

SQL only counts and sums. The completion rule, the outcome vocabulary, the unknown station, the means, the rates, the
range rules and the freshness lag live in `internal/analytics/report`, a pure package that imports no other internal
package (arch test), is in the coverage scope and in `make mutation`. ONE contract suite runs against the in-memory
twin and against real Postgres.

No metric of the brief is dropped: the induced stop count is published (`assignedStops`), the stop count of a route is
published (`RoutePlanned.stopCount`) and the declared cost is published (`WaveCostDeclared`).

### 3. Delivery policy (the projector)

- Consumer group `ANALYTICS_CONSUMER_GROUP` (default `last-mile-delivery-analytics`), fixed and stable; at-least-once,
  the offset is committed only after the message was handled. A brand-new group starts at the earliest offset, so the
  model rebuilds from retained history.
- **Dedupe and projection in ONE analytical-database transaction**: `INSERT ... ON CONFLICT DO NOTHING` into
  `analytics_processed_events`; the fact is written (appended or upserted) only if that insert claimed the id. A failure
  leaves neither; a replay of an applied id is a no-op.
- Not a CloudEvents 1.0 message, or a CloudEvent of a type that is neither projected nor the acknowledged
  `PackageInductionDeferred`: skipped and committed past with a **sampled WARN** (the first, then at most one per
  minute with the count suppressed).
- **Dead-letter ONLY deterministic poison**: an undecodable payload, a subject that is not the aggregate id, a missing
  required field or timestamp, an outcome outside the vocabulary, or a store rejection in Postgres class 22 (data
  exception) or 23 (integrity violation). The raw message goes to `retailnetwork.last-mile-delivery.analytics.dlq` with
  `x-dlq-*` headers, then the offset is committed. A failed DLQ write is retried, so poison is never lost. The DLQ is
  not `<topic>.dlq`: those names belong to the sibling consumers of this context's topics.
- **Transient failures** (database down, timeout, any other error) are retried forever with capped exponential backoff
  (200ms doubling to 5s) and never dead-lettered: a database outage must not turn into silently missing analytics. The
  cost is a blocked partition during the outage, visible in the logs and in `/reports/freshness`.

### 4. The binaries

- `cmd/lm-projector`: the ONLY writer of the analytical database. Env: `ANALYTICS_DATABASE_URL` (required, a direct DSN:
  it runs the embedded migrations), `KAFKA_BROKERS` (required), `ANALYTICS_CONSUMER_GROUP`, `ADMIN_ADDR` (default
  `:8091`, `/healthz` and `/readyz`), `LOG_LEVEL`, `OTEL_SERVICE_NAME` (default `last-mile-delivery-projector`),
  `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT`. It never opens the OLTP database. The migrations are
  embedded (`analytics/migrations.go`, `//go:embed migrations/*.sql`), so there is no `ANALYTICS_MIGRATIONS_PATH`.
- `cmd/lm-reports`: read-only HTTP on `HTTP_ADDR` (default `:8092`): `GET /reports/route-efficiency`,
  `GET /reports/attempt-outcomes`, `GET /reports/wave-utilisation`, `GET /reports/freshness`, `/healthz`, `/readyz`
  (analytical-database ping). It reads `ANALYTICS_READER_DATABASE_URL`, falling back to `ANALYTICS_DATABASE_URL` for
  single-role local runs, through a pool whose sessions default to read-only transactions with a 15s statement timeout.
  `LOG_LEVEL`, `OTEL_SERVICE_NAME` (default `last-mile-delivery-reports`), `OTEL_EXPORTER_OTLP_ENDPOINT`,
  `SERVICE_VERSION`, `ENVIRONMENT`. No Kafka, no OLTP database, no call to any sibling, no auth (fleet rule). Errors are
  problem+json. The routes live in `apis/openapi.yaml` under the tag `reports` with a per-path server on `:8092`.
- Both ship in the one image (`/app/lm-projector`, `/app/lm-reports`).

### 5. Why a separate analytical database

The projection is a derived copy that can be dropped and rebuilt from the topics. Keeping it out of the OLTP database
means a heavy report cannot take OLTP connections, the two schemas evolve independently, the reports role can be
read-only on a database that holds nothing transactional, and the projector holds no OLTP credentials. Both DSNs are
direct (PgBouncer is not involved: the projector runs migrations).

### 6. Packaging

The chart gains an opt-in `analytics:` block (default `enabled: false`; the default render is byte-identical):
components `analytics-projector` (Deployment, no Service) and `analytics-reports` (Deployment + ClusterIP Service, an
opt-in Kong HTTPRoute `/api/last-mile-delivery/reports` rewritten to `/reports`), an `analytics` Secret or
`analytics.database.existingSecret`. Render-time guards refuse `analytics.enabled` without a DSN source, without
`kafka.brokers` or without a consumer group. The projector pod never gets the OLTP `DATABASE_URL`; the reports pod gets
neither Kafka nor any DSN but the reader's.

## Consequences

- Both own topics gain one service-owned consumer group (`last-mile-delivery-analytics`); their contracts are
  unchanged. The reports are only as fresh as the projector, which `/reports/freshness` makes observable.
- The model starts at the earliest retained offset; waves and routes before that have no history.
- `routesCompleted` counts fully attempted routes, not the explicit `COMPLETED` transition (it raises no event). A
  completed route whose `RoutePlanned` was not projected (yet) is not reported, because its stop count is unknown.
- `meanAttemptsPerPackage` and `packagesAttempted` are cohorts of the first-attempt day: a later re-attempt raises the
  mean of the cohort's day after the fact.
- `wavesScheduled`, `stopCapacity` and `stopsInducted` include cancelled waves (`wavesCancelled` is reported beside
  them); a package re-inducted after a cancellation (G17) is counted in both waves' inducted stops.
- A deploy needs a second database, two roles (projector read-write, reports read-only), the DLQ topic and the consumer
  group above (`warehouse-infra`).

## Alternatives considered and rejected

- **Project into the OLTP database**: no isolation of load, schema or credentials.
- **Read sibling facts (shipping-and-tracking's scans, node-topology's stations)**: a sibling's topic; every KPI here is
  defined on this context's own waves, routes and attempts.
- **Fan the facts out to the analytics topic (`FanoutEncoder`)**: every fact the KPIs need is already on one of the two
  own topics; reading both is simpler than doubling the messages and changing the outbox identity.
- **Add a RouteCompleted event**: a new published contract for analytics alone, when the completion precondition is
  already observable from the attempts.
- **Dead-letter after N attempts for every failure**: drops analytics for events that failed only because the database
  was briefly down.
- **Day by CloudEvents `time`** for attempts and waves: that is the moment the use case ran, not when the driver made the
  attempt or the day the wave serves; the payload instants are the business facts.
