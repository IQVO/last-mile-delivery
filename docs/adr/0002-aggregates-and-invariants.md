# ADR 0002: the DeliveryWave, Route, DeliveryServicePartner and PackageAttemptHistory aggregates, their invariants and the assumptions the model left open

## Status

Accepted (2026-10-23).

## Context

The model (retail-network-docs `model/model.yaml`, context `last-mile-delivery`)
gives three aggregates (DeliveryWave, Route, DeliveryServicePartner), nine
invariants-with-names and six published events. The Gap Register items G5, G11,
G16, G17 and G34 and the orchestrator's contracts-delta (E5, E10, E15) change or
complete them. This ADR is the record of what is built and of every assumption
the model and the register leave open. Assumptions are numbered A1.. and are final
unless the orchestrator overrules them.

## Decision

### Packages and aggregates

| Package | Aggregate (root id) | States | Raises |
|---|---|---|---|
| `internal/domain/wave` | DeliveryWave (`waveId`) | OPEN, INDUCTION_CLOSED, DISPATCHED, COMPLETED, CANCELLED | DeliveryWaveScheduled, DeliveryWaveCapacityChanged, DeliveryWaveCancelled |
| `internal/domain/route` | Route (`routeId`) | PLANNED, OUT_FOR_DELIVERY, COMPLETED | RoutePlanned, PackageOutForDelivery, DeliveryAttempted |
| `internal/domain/partner` | DeliveryServicePartner (`dspId`) | ACTIVE, INACTIVE | none |
| `internal/domain/history` | PackageAttemptHistory (`trackingNumber`) | AT_STATION, INDUCTED, DELIVERED, RETURN_INITIATED | PackageInductionDeferred, PackageReturnInitiated |
| `internal/domain/shared` | none | none | the Event interface, Header, shared errors, id and text checks |

Every aggregate is pure Go (no I/O; clock and id are passed in), has a
`Snapshot`/`Restore` pair (Restore re-validates every invariant and returns
`shared.ErrInvalidSnapshot` on a corrupt row) and an optimistic `version`: a
create is version 1, every command adds one, and a repository saves only when the
stored version is `Version()-1`.

### Transitions

- DeliveryWave: ScheduleWave creates OPEN; OPEN -> INDUCTION_CLOSED (CloseInduction);
  INDUCTION_CLOSED -> DISPATCHED (DispatchWave); DISPATCHED -> COMPLETED
  (CompleteWave); OPEN -> CANCELLED (CancelWave). InductPackage keeps OPEN and adds
  an induction. Every other (state, command) pair is `ErrIllegalTransition`,
  except InductPackage, which is `ErrInductionClosed` in any state but OPEN.
- Route: PlanRoute creates PLANNED; PLANNED -> OUT_FOR_DELIVERY (StartRoute);
  OUT_FOR_DELIVERY -> COMPLETED (CompleteRoute). RecordAttempt keeps
  OUT_FOR_DELIVERY (and completes the route when it records the last package).
- DeliveryServicePartner: ContractPartner creates ACTIVE; ACTIVE -> INACTIVE
  (DeactivatePartner, G34).
- PackageAttemptHistory: Arrive creates INDUCTED or AT_STATION; AT_STATION ->
  INDUCTED (Place); INDUCTED -> AT_STATION (Unplace); INDUCTED -> INDUCTED or
  AT_STATION or RETURN_INITIATED (RecordFailure); INDUCTED -> DELIVERED
  (RecordDelivered).

### Invariants (the model's Err names, plus the ones this ADR adds)

| Error | Aggregate | Rule | HTTP problem |
|---|---|---|---|
| `ErrInvalidWaveTimes` | wave | `inductCutoffAt < windowStart < windowEnd` | 422 `invalid-wave-times` |
| `ErrWaveFull` | wave | assigned stops `<=` stop capacity | 409 `wave-full` (policy only) |
| `ErrInductionClosed` | wave | no package joins after induction closes (or after the cutoff) | 409 `induction-closed` (policy only) |
| `ErrRouteOverCapacity` | route | stops `<=` the partner's van capacity | 409 `route-over-capacity` |
| `ErrRouteNotStarted` | route | an attempt only on an OUT_FOR_DELIVERY route | 409 `route-not-started` |
| `ErrAlreadyDelivered` | route, history | a package is delivered at most once | 409 `already-delivered` |
| `ErrInvalidVanCapacity` | partner | van capacity `> 0` | 422 `invalid-van-capacity` |
| `ErrUnknownStation` | use case (StationView) | the node is a known DELIVERY_STATION (A1) | 422 `unknown-station` |
| `ErrPackageNotOnRoute` | route | the attempted package is on the route | 404 `package-not-on-route` |
| `ErrAttemptAlreadyRecorded` | route | one attempt per package per route | 409 `attempt-already-recorded` |
| `ErrPackageNotInducted` | route | a route holds only packages inducted in its wave (G34) | 409 `package-not-inducted` |
| `ErrPackageAlreadyRouted` | route | a package is on at most one route of a wave | 409 `package-already-routed` |
| `ErrStopKeyMismatch` | route | the packages of one stop share one stopKey (G11) | 409 `stop-key-mismatch` |
| `ErrPartnerInactive` | route | a route is given only to an ACTIVE partner | 409 `partner-inactive` |
| `ErrPartnerStationMismatch` | route | the partner works for the wave's station | 409 `partner-station-mismatch` |
| `ErrRouteIncomplete` | route | a route completes only when every package has an attempt | 409 `route-incomplete` |
| `shared.ErrValidation` | all | a value-object rule | 422 `validation-failed` |
| `shared.ErrIllegalTransition` | all | the command is not allowed from the current state | 409 `illegal-transition` |
| `ports.ErrConcurrentUpdate` | repository | version mismatch | 409 `concurrent-update` |
| `ports.ErrNotFound` | repository | an addressed aggregate does not exist | 404 `not-found` |

### Assumptions (where the model and the register are silent)

- **A1** (G34) `ScheduleWave` and `ContractPartner` require `stationNodeId` to be a
  `DELIVERY_STATION` in the `StationView` read model (the pack lists the check
  though no invariant requires it). An unknown or non-station id is
  `ErrUnknownStation` (422). The check lives in the use case because the view is
  an application read model.
- **A2** `serviceDate` is the station's local calendar date. It is validated as a
  `YYYY-MM-DD` date and not cross-checked against `windowStart` and the station's
  time zone.
- **A3** `stopCapacity` is an integer from 0 to 2147483647 (a plain literal bound
  for the Postgres `integer`); zero is allowed (the model's OpenAPI minimum).
- **A4** (G11) Capacity is counted in STOPS, not packages: `assignedStops` is the
  number of distinct `stopKey`s among the wave's inductions. A package joining a
  stop already in the wave adds no stop, so it raises no
  `DeliveryWaveCapacityChanged`. A package with no label data is its own stop
  with the key `pkg:<trackingNumber>`.
- **A5** `InductPackage` is accepted only when the wave is OPEN and the instant is
  before `inductCutoffAt`, else `ErrInductionClosed`; a new stop on a full wave is
  `ErrWaveFull`; the same tracking number twice in one wave is the internal
  `ErrAlreadyInducted`, which the use case treats as a no-op.
- **A6** Eligible wave search (policy InductOnArrival): the OPEN waves of the
  package's station whose `inductCutoffAt` is after the instant, ordered by
  `inductCutoffAt` then `waveId`; the first that accepts wins; `ErrWaveFull` and
  `ErrInductionClosed` move on to the next wave; `ErrConcurrentUpdate` retries the
  whole attempt once (the `transact` helper of node-topology).
- **A7** The instant of an induction is `arrivedAt` for `LoadArrived`, the attempt's
  `at` for a failed attempt, and the clock's now for a wave cancel, a dispatch
  leftover and the deferred sweep.
- **A8** `LoadArrived` is acted on when its `nodeType` is `DELIVERY_STATION`; when
  `nodeType` is absent (a producer that predates contracts-delta E3) the
  `StationView` decides; anything else is ignored.
- **A9** (G34) A wave completes (DISPATCHED -> COMPLETED) in the same transaction in
  which its last route completes (every route of the wave COMPLETED). A wave
  dispatched with no route has nothing to run and completes at once, so the
  dispatch response may show COMPLETED.
- **A10** (G17) `CancelWave` stays OPEN -> CANCELLED only (the model's transition); a
  reason of 1 to 200 characters is required. In the same transaction every
  package inducted in the wave is re-inducted onto the next eligible wave (A6, A7);
  a package with no eligible wave is unplaced (A19). The cancelled wave keeps its
  induction list as a record.
- **A11** `DispatchWave` re-inducts, by the same mechanism, the packages inducted in
  the wave that no route of the wave holds (they would otherwise be stranded).
- **A12** (G34) `PlanRoute` requires: the wave INDUCTION_CLOSED
  (`ErrIllegalTransition` otherwise), the partner ACTIVE (`ErrPartnerInactive`) and
  of the wave's station (`ErrPartnerStationMismatch`), stop numbers 1..n without
  gaps or duplicates, a tracking number at most once per route, every package
  inducted in the wave (`ErrPackageNotInducted`) and on no other route
  (`ErrPackageAlreadyRouted`), and one stopKey per stop (`ErrStopKeyMismatch`). The
  van capacity is read once at planning; a later partner change re-checks nothing.
- **A13** `Route.stationNodeId` is copied from the wave at planning.
- **A14** `StartRoute` requires the route's wave to be DISPATCHED
  (`ErrIllegalTransition` otherwise); the use case reads the wave, it does not change it.
- **A15** `RecordAttempt` accepts one attempt per package per route: a second after
  a DELIVERED is `ErrAlreadyDelivered`, after a FAILED `ErrAttemptAlreadyRecorded`.
  `failureReason` is free text (the model defines no vocabulary) of 1 to 200
  characters, required when FAILED and forbidden when DELIVERED. `at` must not be
  zero and is stored in UTC; it is not compared with the start of the route.
- **A16** (G34) A route completes inside `RecordAttempt` in the same save as the
  attempt that records its last outstanding package (policy
  CompleteRouteOnLastAttempt); `CompleteRoute` therefore has no REST operation. A
  route whose driver never records every package stays OUT_FOR_DELIVERY (listed in
  `model_gaps`).
- **A17** (G17) A stop's outcome is derived: unset until every package of the stop has
  an attempt, then DELIVERED when all were delivered, else FAILED.
- **A18** (G16) `PackageAttemptHistory` is a fourth aggregate, one per tracking number
  (entity `packageattempthistory`), created when the package first reaches the
  station. It counts failed attempts; `MaxFailedAttempts` is 3: the third failure
  raises `PackageReturnInitiated` and ends in RETURN_INITIATED without
  re-induction. DELIVERED and RETURN_INITIATED are terminal; a later arrival of the
  package is ignored. A package already INDUCTED ignores a repeated arrival.
- **A19** (G16) `PackageInductionDeferred` is raised each time a package that is at
  the station finds no wave: on arrival, after a failed attempt, after a wave
  cancel and for a dispatch leftover. The package is then AT_STATION. Policy
  InductDeferredOnSchedule: when a wave is scheduled, the AT_STATION packages of its
  station are inducted into it, oldest arrival first, until the wave is full; a
  package that still finds no wave raises nothing again.
- **A20** (G16) `assignmentId` on `PackageInductionDeferred` is copied from the package's
  `LabelIssued` (the `rm_package_stops` read model) when the history is created, and
  omitted when no label data was received.
- **A21** Idempotency (D3): `idempotency_keys` keyed by (route, key), TTL 24 h; only a
  2xx response is stored and replayed; a key reused with a different request body
  is `422 validation-failed`.
- **A22** Streams: `RoutePlanned` goes ONLY to `.analytics`; all other published events
  go ONLY to `.events` (including `DeliveryWaveCancelled`, G5).
- **A23** Subject and Kafka key: `waveId` for wave events, `routeId` for route events,
  `trackingNumber` for history events.
- **A24** Wire types: ids and tracking numbers are strings; instants RFC 3339 UTC
  strings; `serviceDate` a date; counts integers; `outcome` DELIVERED|FAILED; optional
  fields omitted. They are written in `apis/asyncapi.yaml` and `apis/openapi.yaml`.
- **A25** `DeactivatePartner` is ACTIVE -> INACTIVE only (no reactivation, no event);
  an INACTIVE partner is given no new route, planned routes are unaffected.
- **A26** REST additions to the model's list (documented in `apis/openapi.yaml`):
  `GET /waves/{waveId}/stops` (stops grouped by stopKey, G11),
  `POST /waves/{waveId}:cancel` (G17), `GET /routes/{routeId}`,
  `POST /partners/{dspId}:deactivate` (G34).
- **A27** Ids are minted as a prefix and a UUID v4 (`wave-`, `route-`, `dsp-`); the
  opaque id check allows 1 to 64 characters of `A-Z a-z 0-9 . _ : -`.
- **A28** `ServiceAreaAssigned` is consumed into `rm_station_service_areas` as the
  model says, but no decision of this context reads it yet.
- **A29** A wave's inductions are stored with the wave (one row per package); a wave
  holds at most one stop per unit of capacity and any number of packages per stop,
  so its size is bounded by the packages a station receives per wave.

## Consequences

- Every rule above has a table-driven domain test; the transitions are tested both
  ways (legal and illegal).
- The policies that touch several aggregates (re-induction, wave completion,
  deferred sweep) run in ONE unit of work with the outbox, so a crash cannot lose
  a failed attempt or a deferral.
- The orchestrator is asked to back-fill the model with: the fourth aggregate, the
  four REST additions, `DeactivatePartner`, the two G16 events and the stop-based
  capacity.
