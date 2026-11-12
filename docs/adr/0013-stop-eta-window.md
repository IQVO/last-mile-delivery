# ADR 0013: the day-of stop ETA window (StopEtaWindowAssigned at StartRoute; roadmap E3 provider)

## Status

Accepted (2026-11-11).

## Context

A shopper whose parcel is out for delivery sees only the promised delivery
window, which can be a whole day. The network's plan for E3 is a day-of ETA
window of two hours, relayed by shipping-and-tracking and shown by
customer-ordering on the tracking view. This context is the provider: it owns
the route, its stop order and the wave whose delivery window bounds the day.

What the code gives it, and what it does not:

- There is no stop sequencer. The station planner sends the numbered stops
  (`PlanRoute`); `route.Plan` only validates them. The sequence is the planner's.
- `StartRoute` already loads the wave (`DISPATCHED` is required) and the wave
  carries `windowStart`, `windowEnd` and every induction with its `stopKey`.
- The context holds no geography, no travel time and no service time. A
  routing-based ETA is therefore not possible here without a new data source.

Alternatives weighed:

- **Derive the ETA from coordinates and travel time.** Rejected: nothing in
  the context or in its consumed contracts carries a coordinate (PII
  confinement keeps the address in customer-ordering and
  shipping-and-tracking, and the stop is the opaque `stopKey`).
- **Persist the ETA on the route and serve it on `GET /routes/{routeId}`.**
  Rejected: `Start` is a single transition that commits with its outbox rows
  (a second `StartRoute` is `409 illegal-transition`), so there is nothing to
  replay or recompute and no new aggregate field is needed.
- **Re-raise or retract the ETA as the day goes on.** Rejected for this slice:
  without a position signal nothing would change the answer.
- **Publish one event per package.** Rejected: the stop is the unit the driver
  visits and the unit of the computation; the event carries the packages of the
  stop and consumers fan out by tracking number.

## Decision

1. **Event `StopEtaWindowAssigned`** (integration, events topic).
   - Type `com.retailnetwork.logistics.last-mile-delivery.route.StopEtaWindowAssigned`;
     source `/retailnetwork/last-mile-delivery`; dataschema
     `urn:retailnetwork:last-mile-delivery:events:StopEtaWindowAssigned:v1`;
     topic `retailnetwork.last-mile-delivery.events`; entity `route`; subject
     and Kafka key = `routeId` (the same key as `PackageOutForDelivery`, so the
     two stay ordered on one partition).
   - data: `routeId` (string), `stopNo` (integer >= 1), `stopKey` (string,
     opaque; `pkg:<trackingNumber>` when the package has no label data),
     `trackingNumbers` (string[], at least 1, the packages of the stop in stop
     order), `etaStart` and `etaEnd` (RFC 3339 UTC). CloudEvents `time` = the
     route start instant = `assignedAt`; shipping-and-tracking relies on this.
   - Raised by `StartRoute`, in the same transaction and AFTER every
     `PackageOutForDelivery`, one per stop in stop order. An ETA is never
     retracted and is never re-raised for the same start; when the wave's
     window leaves less than `MinEtaSpan` at the start instant NO event is
     raised (no ETA). A package that fails and is re-inducted gets a new ETA
     from its next `StartRoute`.
   - The plan's field list is extended by `stopNo`, because a route may hold two
     stops with the same `stopKey` (only the packages within ONE stop must share
     one), so the plan's fields alone do not identify a stop.
2. **The computation** is a pure function in `internal/domain/route/route_eta.go`
   with named constants and integer duration arithmetic only (no float):

   | constant | value |
   | --- | --- |
   | `EtaWindowWidth` | 2 hours |
   | `MinEtaSpan` | 30 minutes |
   | `EtaGrain` | 1 minute |

   `EtaWindows(in EtaInput{Now, WindowStart, WindowEnd, Stops []StopRef{No, StopKey, TrackingNumbers}}) []StopEta`:
   1. `from = max(Now, WindowStart)`, `to = WindowEnd`, `span = to - from`. If
      `span < MinEtaSpan` return nothing.
   2. `width = min(EtaWindowWidth, span)`, truncated to `EtaGrain`.
   3. For stop `k` of `n`: `centre = from + (span / (2n)) * (2k - 1)` (no
      overflow: the product is at most `span`). `start = centre - width/2`; if
      `start < from` then `start = from`; if `start + width > to` then
      `start = to - width`; truncate `start` to `EtaGrain`; `end = start + width`.

   The stops are spread linearly over the wave's delivery window: this is a
   deterministic spread, not a prediction. Its quality is unproven; the KPI of
   point 5 decides whether it is good enough.
3. **`Route.Start(now, wv)`** replaces `Start(now)`. It builds the `StopRef`s
   from the route's stops and the wave induction's `stopKey` of the stop's first
   package (a package without an induction is `shared.Corrupt`, impossible after
   `Plan`) and appends the ETA events after the `PackageOutForDelivery` ones.
   `StartRoute.Execute` passes the wave it already loaded. No aggregate field is
   added and nothing about the ETA is persisted on the route.
4. **Observability.** Counters `last_mile_delivery.stop_eta.assigned` (one per
   event) and `last_mile_delivery.stop_eta.skipped` (attribute
   `reason=window_elapsed`), through a new small port `ports.EtaMetrics` with the
   nil-safe pattern of `Deps.metrics()`.
5. **The KPI: share of deliveries inside the ETA window** (analytical side,
   ADR 0012).
   - Analytical migration `analytics/migrations/0002_stop_eta_windows`:
     `stop_eta_windows(route_id, tracking_number, stop_no, eta_start, eta_end,
     assigned_at, event_id, PRIMARY KEY (route_id, tracking_number))`, one row
     per package, exploded at projection by one idempotent statement
     (`INSERT ... SELECT unnest(...) ... ON CONFLICT DO NOTHING`), plus an index
     on `(tracking_number)`.
   - The projector maps the full type to `report.KindStopEtaWindowAssigned`;
     `Event.Validate` requires `stopNo >= 1`, at least one tracking number and
     `etaStart < etaEnd`. The type leaves the acknowledged-only list.
   - Report `GET /reports/eta-window` (operationId `getEtaWindowReport`, same
     `from`/`to` parameters, `x-pagination-exempt: report`, served by
     `cmd/lm-reports`): per UTC day of the DELIVERED attempt and per station
     (`UNKNOWN` if not projected): `deliveriesWithEta`, `deliveriesInsideEta`,
     `deliveriesBeforeEta`, `deliveriesAfterEta` and `etaHitRate = inside /
     withEta` (null when no delivery had an ETA). Inside means
     `eta_start <= attempted_at <= eta_end`. Only the attempt that DELIVERED
     counts; a delivered package whose ETA row was never projected is not in
     the denominator.
6. **Contract registration.** `apis/asyncapi.yaml` (message, publish operation,
   description counts), the type catalogue ADR 0004 (table row and the
   `type:` line), `docs/docs/api-reference/events.md`, the domain pages and the
   ADR index move with the code. The encoder payload lives in a new file
   `encoder_eta.go`.
7. **What this does not do.** It does not change any promise, group or
   judgement; the ETA never reaches delivery-promise. A window that falls after
   `deliverByLatest` is published as computed (no clipping). Nothing carries an
   address, a name, a phone or free text; `stopKey` is the opaque HMAC.

## Consequences

- No OLTP migration and no aggregate field. `GET /routes/{routeId}` and the MCP
  and A2A `get_route` do NOT show the ETA: the event and the E3 report are the
  record.
- The integration topic gains one type (nine on the events topic, ten in all);
  the catalogue and the contract moved in one commit.
- A route start late in the wave window, or a window of less than 30 minutes,
  yields no ETA at all (counted as `skipped`); the route starts normally.
- The first month of `etaHitRate` is a baseline, not a verdict: if the linear
  spread proves poor, a routing signal has to come from outside this context
  (a new event from the planner), as a new dataschema or an additive field.
- Deploy: no env var, topic, consumer group, chart value or secret. The
  analytical migration 0002 runs when `cmd/lm-projector` boots. Deploy order
  across the chain is customer-ordering, shipping-and-tracking, then this
  service: this service's deploy is the activation of the ETA events, because a
  consumer ignores a type it has no handler for and commits past it.
- Rollback: LM rollback stops the events; consumers see only silence.
