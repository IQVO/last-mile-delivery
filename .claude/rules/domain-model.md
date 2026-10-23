# Domain model: ubiquitous language, aggregates, events, use cases

Source of truth: retail-network-docs `model/model.yaml` (context `last-mile-delivery`,
a Supporting context of the logistics tier). Invariants, transitions and every
assumption the model left open are in `docs/adr/0002-aggregates-and-invariants.md`
(assumptions A1-A29). Consumed contracts and the local read models are in
`docs/adr/0003-consumed-contracts-and-local-read-models.md`.

## Ubiquitous Language (use these exact names)

- **Delivery station** — a node of type `DELIVERY_STATION`, known only through the
  event-fed `StationView` (`rm_stations`); never called synchronously.
- **DeliveryWave** — a delivery window of one station on one service date: induct
  cutoff, window start and end, **stop capacity**. Holds the packages inducted into it.
- **Induction** — placing an arrived package into the earliest OPEN wave whose cutoff
  it arrived before and that still has stop capacity. Capacity counts **stops**, not
  packages: packages sharing a `stopKey` take one stop (A4).
- **Stop / stopKey** — packages for one drop-off point, grouped by the opaque
  `stopKey` shipping-and-tracking derives at label time (`LabelIssued`). No address,
  name or any PII is ever held or published; a package is identified by tracking number.
- **Route** — one DSP van run over the stops of a dispatched wave; state PLANNED,
  OUT_FOR_DELIVERY, COMPLETED. Outcomes are per package; a stop's outcome is derived.
- **DeliveryServicePartner (DSP)** — a van operator of one station with a van
  capacity (stops per route); ACTIVE or INACTIVE.
- **PackageAttemptHistory** — per tracking number: failed attempts (max 3), status
  AT_STATION, INDUCTED, DELIVERED, RETURN_INITIATED. Makes "delivered at most once"
  hold across routes (G16).

## Aggregates

- **DeliveryWave** (`internal/domain/wave`): `inductCutoffAt < windowStart <
  windowEnd` (`ErrInvalidWaveTimes`); assigned stops never exceed capacity
  (`ErrWaveFull`); no induction unless OPEN (`ErrInductionClosed`); OPEN ->
  INDUCTION_CLOSED -> DISPATCHED -> COMPLETED, OPEN -> CANCELLED.
- **Route** (`internal/domain/route`): stops `<=` van capacity (`ErrRouteOverCapacity`);
  only packages inducted in the wave, each on one route; one stop-key per stop; an
  attempt only when OUT_FOR_DELIVERY (`ErrRouteNotStarted`); one attempt per package
  (`ErrAttemptAlreadyRecorded`); delivered at most once (`ErrAlreadyDelivered`).
- **DeliveryServicePartner** (`internal/domain/partner`): van capacity `> 0`
  (`ErrInvalidVanCapacity`); raises no event (model); deactivation is G34.
- **PackageAttemptHistory** (`internal/domain/history`): the third failed attempt
  returns the package to its sender; a package with no wave to join is deferred.
- `internal/domain/shared`: `Event`, `Header`, `ErrValidation`,
  `ErrIllegalTransition`, `ErrAlreadyDelivered`, `ErrInvalidSnapshot`, id and
  stop-key checks.

Every aggregate has `Snapshot()`, a validating `Restore(Snapshot)` and an
optimistic `Version()`; `now` and ids are passed in. `ErrNotFound` and
`ErrConcurrentUpdate` are port errors (`internal/application/ports/errors.go`).
The domain imports nothing outside `internal/domain`.

## Domain events

Eight published events (`internal/domain/*/events.go`; wire shape in
`apis/asyncapi.yaml`, catalogue in ADR 0004):

- `DeliveryWaveScheduled`, `DeliveryWaveCapacityChanged`, `DeliveryWaveCancelled`
  (wave, `internal/domain/wave/events.go`).
- `RoutePlanned` (analytics topic ONLY), `PackageOutForDelivery`,
  `DeliveryAttempted` (route, `internal/domain/route/events.go`).
- `PackageInductionDeferred`, `PackageReturnInitiated`
  (`internal/domain/history/events.go`).

## Key use cases (`internal/application/usecases`)

One per command and query; commands run in ONE `ports.UnitOfWork` (load, domain
call, save, outbox append) and retry once on `ErrConcurrentUpdate`.

- Waves (`waves.go`): `ScheduleWave`, `CloseInduction`, `DispatchWave`, `CancelWave`.
- Routes (`routes.go`): `PlanRoute`, `StartRoute`, `RecordAttempt`.
- Partners (`partners.go`): `ContractPartner`, `DeactivatePartner`.
- Consumed-event policies (`consumers.go`, inbox-claimed): `InductOnArrival`
  (`LoadArrived`), `ApplyNodeRegistered`, `ApplyServiceAreaAssigned`,
  `ApplyLabelIssued`. Induction itself is `induct.go`.
- Queries (`queries.go`): `ListWaves`, `GetWaveStops`, `GetRoute`. Read DTOs are in
  `internal/application/views`; filters in `internal/application/repository`.
- Ports (`internal/application/ports/ports.go`) are interfaces only; the memory test
  doubles in `internal/adapters/outbound/memory` must pass the same suite as Postgres
  (`internal/adapters/outbound/repocontract`).
