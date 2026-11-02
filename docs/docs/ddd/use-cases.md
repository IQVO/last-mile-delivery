---
id: use-cases
title: Use cases
sidebar_label: Use cases
sidebar_position: 1
---

# Use cases

One application service per command or policy, in `internal/application/usecases`. Every writing use case runs inside one `UnitOfWork` (`transact`): it loads at most one aggregate to change, saves it, and writes its events to `outbox_events` in the same Postgres transaction; a relay publishes them afterwards ([Architecture](/docs/overview/architecture)). Three read use cases (`ListWaves`, `GetWaveStops`, `GetRoute`) are shared by REST, MCP and A2A. Step-by-step diagrams are on [Sequence diagrams](/docs/ddd/sequence-diagrams); the wire contract is [`apis/openapi.yaml`](https://github.com/IQVO/last-mile-delivery/blob/develop/apis/openapi.yaml). Decision ids (`A…`, `G…`) refer to [ADR 0002](/docs/adr/0002-aggregates-and-invariants) and the gap register.

Source: `internal/application/usecases/{waves,routes,partners,induct,consumers,queries}.go`, `internal/adapters/inbound/http/{server,errors}.go`, `internal/adapters/inbound/kafka/consumers.go`.

## Triggers at a glance

| Use case | Aggregate | Trigger | Inbound adapter |
| --- | --- | --- | --- |
| `ScheduleWave` | `DeliveryWave` (+ waiting `PackageAttemptHistory`) | station planner | REST `POST /waves` |
| `CloseInduction` | `DeliveryWave` | station planner | REST `POST /waves/{waveId}/induction-closures` |
| `DispatchWave` | `DeliveryWave` (+ leftovers, completion) | station planner | REST `POST /waves/{waveId}/dispatches` |
| `CancelWave` | `DeliveryWave` (+ re-induction) | station planner | REST `POST /waves/{waveId}/cancellations` |
| `PlanRoute` | `Route` (reads wave, partner) | station planner | REST `POST /routes` |
| `StartRoute` | `Route` (reads wave) | DSP driver app | REST `POST /routes/{routeId}/starts` |
| `RecordAttempt` | `Route` + `PackageAttemptHistory` (+ wave, completion) | DSP driver app | REST `POST /routes/{routeId}/attempts` |
| `ContractPartner` | `DeliveryServicePartner` | station planner | REST `POST /partners` |
| `DeactivatePartner` | `DeliveryServicePartner` | station planner | REST `POST /partners/{dspId}/deactivations` |
| `InductOnArrival` (policy) | `PackageAttemptHistory`, `DeliveryWave` | `LoadArrived` | Kafka consumer `linehaul` |
| `ApplyNodeRegistered`, `ApplyServiceAreaAssigned` | read models `rm_stations`, `rm_station_service_areas` | `NodeRegistered`, `ServiceAreaAssigned` | Kafka consumer `node-topology` |
| `ApplyLabelIssued` | read model `rm_package_stops` | `LabelIssued` | Kafka consumer `shipping` |
| `ListWaves`, `GetWaveStops`, `GetRoute` | none (reads) | any client | REST `GET`, MCP tools, A2A skills |

Policies that run inside other use cases: `InductDeferredOnSchedule` (inside `ScheduleWave`), `RetryNextWave` (inside `RecordAttempt`), `CompleteWaveOnLastRoute` (inside `DispatchWave` and `RecordAttempt`), re-induction of leftovers (inside `DispatchWave` and `CancelWave`).

## Error mapping

REST errors are RFC 9457 `application/problem+json`; the `type` is the slug (`internal/adapters/inbound/http/errors.go`).

| Error | HTTP | Problem `type` |
| --- | --- | --- |
| malformed body, `shared.ErrValidation` | 422 | `validation-failed` |
| `wave.ErrInvalidWaveTimes` | 422 | `invalid-wave-times` |
| `partner.ErrInvalidVanCapacity` | 422 | `invalid-van-capacity` |
| `usecases.ErrUnknownStation` | 422 | `unknown-station` |
| missing `Idempotency-Key` | 400 | `idempotency-key-required` |
| `Idempotency-Key` not 1 to 128 printable ASCII characters | 400 | `idempotency-key-invalid` |
| key reused with another method, path or body | 409 | `idempotency-key-reused` |
| bad `limit`, bad `cursor`, undeclared query parameter | 400 | `invalid-limit`, `invalid-cursor`, `unknown-query-parameter` |
| `ports.ErrNotFound` | 404 | `not-found` |
| `route.ErrPackageNotOnRoute` | 404 | `package-not-on-route` |
| `wave.ErrWaveFull` | 409 | `wave-full` |
| `wave.ErrInductionClosed` | 409 | `induction-closed` |
| `wave.ErrAlreadyInducted` | 409 | `already-inducted` |
| `route.ErrRouteOverCapacity` | 409 | `route-over-capacity` |
| `route.ErrRouteNotStarted` | 409 | `route-not-started` |
| `shared.ErrAlreadyDelivered` | 409 | `already-delivered` |
| `route.ErrAttemptAlreadyRecorded` | 409 | `attempt-already-recorded` |
| `route.ErrPackageNotInducted` | 409 | `package-not-inducted` |
| `route.ErrPackageAlreadyRouted` | 409 | `package-already-routed` |
| `route.ErrStopKeyMismatch` | 409 | `stop-key-mismatch` |
| `route.ErrPartnerInactive` | 409 | `partner-inactive` |
| `route.ErrPartnerStationMismatch` | 409 | `partner-station-mismatch` |
| `route.ErrRouteIncomplete` | 409 | `route-incomplete` |
| `shared.ErrIllegalTransition` | 409 | `illegal-transition` |
| `ports.ErrConcurrentUpdate` | 409 | `concurrent-update` |
| anything else | 500 | `internal-error` |

## ScheduleWave

- **Trigger**: station planner, `POST /waves` with `Idempotency-Key`.
- **Input**: `stationNodeId`, `serviceDate` (`YYYY-MM-DD`), `inductCutoffAt`, `windowStart`, `windowEnd`, `stopCapacity`.
- **Invariants**: the station is a known `DELIVERY_STATION` in `rm_stations` (`ErrUnknownStation`); `inductCutoffAt < windowStart < windowEnd` (`ErrInvalidWaveTimes`); valid ids and capacity.
- **Flow**: require the station, `wave.Schedule` builds an `OPEN` wave at version 1, save, then **policy `InductDeferredOnSchedule`**: sweep the station's packages in status `AT_STATION` (oldest first, at most `SweepLimit` = 1000) and induct each into the first eligible wave. Publish; return the wave as saved (the sweep may already have filled stops).
- **Events**: `DeliveryWaveScheduled`, plus one `DeliveryWaveCapacityChanged` per new stop the sweep inducted.

## CloseInduction

- **Trigger**: station planner, `POST /waves/{waveId}/induction-closures`.
- **Invariant**: the wave is `OPEN` (`OPEN` to `INDUCTION_CLOSED`, else `illegal-transition`).
- **Events**: none.

## DispatchWave

- **Trigger**: station planner, `POST /waves/{waveId}/dispatches`.
- **Invariant**: the wave is `INDUCTION_CLOSED` (to `DISPATCHED`).
- **Flow**: dispatch; **re-induct leftovers** (A11): every package inducted in the wave that no route of it holds is placed on the next eligible `OPEN` wave, or becomes `AT_STATION` again; then **`CompleteWaveOnLastRoute`** (A9): complete the wave at once when no route is left uncompleted.
- **Events**: `DeliveryWaveCapacityChanged` on the receiving waves, `PackageInductionDeferred` for a package no wave takes.

## CancelWave

- **Trigger**: station planner, `POST /waves/{waveId}/cancellations` with `reason` (1 to `MaxReason` characters, trimmed).
- **Invariant**: the wave is `OPEN` (to `CANCELLED`).
- **Flow**: cancel, then **re-induct every package it held** onto the next eligible wave (G17); a package no wave takes is deferred.
- **Events**: `DeliveryWaveCancelled` (an integration event, G5), `DeliveryWaveCapacityChanged`, `PackageInductionDeferred`.

## PlanRoute

- **Trigger**: station planner, `POST /routes`.
- **Input**: `waveId`, `dspId`, `stops[]` of `{stopNo, trackingNumbers[]}`.
- **Invariants** (`route.Plan`, A12): at least one stop, numbered `1..n` in order, each with valid and distinct tracking numbers; the wave is `INDUCTION_CLOSED`; the partner is active and belongs to the wave's station; stops `<=` the partner's `vanCapacity`; every package is inducted in the wave, not on another route of it, and all packages of a stop share one `stopKey`.
- **Events**: `RoutePlanned` (analytics topic only).

## StartRoute

- **Trigger**: DSP driver app, `POST /routes/{routeId}/starts`.
- **Invariants**: the route is `PLANNED` and its wave is `DISPATCHED` (else `illegal-transition`).
- **Events**: one `PackageOutForDelivery` per tracking number, in stop order (A14).

## RecordAttempt

- **Trigger**: DSP driver app, `POST /routes/{routeId}/attempts`.
- **Input**: `trackingNumber`, `outcome` (`DELIVERED` or `FAILED`), `failureReason` (required exactly when `FAILED`), `at`.
- **Invariants**: the route is `OUT_FOR_DELIVERY` (`route-not-started`); the package is on the route (`package-not-on-route`); one attempt per package per route (`attempt-already-recorded`); a delivered package cannot be delivered again, on any route (`already-delivered`, also enforced by a partial unique index).
- **Flow**: record the attempt on the route; update the package's `PackageAttemptHistory`: `DELIVERED` marks it delivered; `FAILED` counts a failure, and unless this would be the third, **`RetryNextWave`** places the package on the next eligible wave (A18, A19). Save, publish, then complete the route when every package has an attempt (A16) and the wave when its last route completes (A9).
- **Events**: `DeliveryAttempted`; on a failure that finds a wave `DeliveryWaveCapacityChanged`, with none `PackageInductionDeferred`; on the third failure `PackageReturnInitiated`.
- **Metric**: the business counter `last_mile_delivery.package.attempted` counts the outcome (`delivered`, `failed`, or `rejected` with the error slug as `reason`).

## ContractPartner and DeactivatePartner

- `ContractPartner` (`POST /partners`): the station must be known (`unknown-station`), `vanCapacity > 0` (`invalid-van-capacity`); saves an `ACTIVE` partner. No event.
- `DeactivatePartner` (`POST /partners/{dspId}/deactivations`, G34): `ACTIVE` to `INACTIVE`; an inactive partner cannot be given a new route (`partner-inactive`). No event.

## InductOnArrival (policy, consumer `linehaul`)

- **Trigger**: `com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived`.
- **Flow**: claim the CloudEvents id in `processed_events` (a repeat is a no-op); ignore a load that did not reach a delivery station (A8: the event's `nodeType` when present, else `rm_stations`); for every valid tracking number record it in `rm_inbound_packages` and `arrive` it: a new package gets its stop key from `rm_package_stops` (or `pkg:<trackingNumber>`), is placed on the first `OPEN` wave whose cutoff is after the arrival (earliest first, skipping full or closed waves), and gets a `PackageAttemptHistory` (`INDUCTED`, or `AT_STATION` when no wave takes it); a package already waiting is retried; one already inducted, delivered or returned is ignored (A18).
- **Events**: `DeliveryWaveCapacityChanged` per new stop, `PackageInductionDeferred` per unplaced package.

## Read-model consumers

- `ApplyNodeRegistered`: claims the event and keeps the node in `rm_stations` only when `nodeType` is `DELIVERY_STATION`.
- `ApplyServiceAreaAssigned`: keeps the assignment in `rm_station_service_areas` when its `assignmentVersion` is higher than the stored one.
- `ApplyLabelIssued`: keeps `stopKey`, `destinationPostalPrefix` and `assignmentId` in `rm_package_stops`; a label without a usable `stopKey` is ignored (A4).

None of them raises events. Deterministic problems (undecodable payload, invalid values) are dead-lettered, transient ones retried ([Runbook](/docs/operations/runbook)).

## Queries

- `ListWaves(stationNodeId, serviceDate)`: the station's waves for the date ordered by `inductCutoffAt` then `waveId`; an invalid station id or a date that is not `YYYY-MM-DD` is `validation-failed`; none is an empty list.
- `GetWaveStops(waveId)`: the wave's packages grouped by `stopKey` in order of first induction (G11), with `destinationPostalPrefix` when the label data has it; `not-found` for an unknown wave.
- `GetRoute(routeId)`: the route with its stops (derived `outcome`) and attempts; `not-found` for an unknown route.

## Not here

Completing a wave or a route has no REST command: both are policies (`CompleteWaveOnLastRoute`, and route completion inside `RecordAttempt`). Packages are inducted only by the `linehaul` consumer and the policies above, never by a REST call.
