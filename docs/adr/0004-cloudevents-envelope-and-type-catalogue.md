# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-23).

## Context

Every Kafka message in the retail network is a CloudEvents 1.0 event in structured
mode (retail-network-docs `model/model.yaml` `conventions.cloudevents`; the repo
rule `.claude/rules/fleet/cloudevents.md`). This ADR is the catalogue of every
type last-mile-delivery publishes. `TestEventCatalogueMatchesContract` checks it
against `apis/asyncapi.yaml`. The consumed types are listed in ADR 0003.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across
  relay retries; consumers dedupe on it); `source`
  `/retailnetwork/last-mile-delivery`; `subject` the aggregate root id (`waveId`,
  `routeId` or, for the per-package history, the `trackingNumber`); `time` the
  instant of the change, in UTC; `datacontenttype` `application/json`;
  `dataschema` `urn:retailnetwork:last-mile-delivery:<stream>:<EventName>:v1`
  with stream `events` or `analytics`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the
  subject, so all events of one aggregate stay ordered on one partition.
- `data` is camelCase; optional fields are omitted when unset; timestamps are
  RFC 3339 UTC; the service date is `YYYY-MM-DD`.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.
- `<entity>` is the raising aggregate, lowercase, no separators: `wave`,
  `route`, `packageattempthistory`. Tier is `logistics`.

### Published types

Seven types go ONLY to the integration topic `retailnetwork.last-mile-delivery.events`
(`stream: events`); `RoutePlanned` is analytics-only and goes ONLY to
`retailnetwork.last-mile-delivery.analytics` (`stream: analytics`).
`DeliveryWaveCancelled` is an integration event (Gap Register G5, contracts-delta
E5), no longer analytics-only. `PackageInductionDeferred` and
`PackageReturnInitiated` are the Gap Register G16 additions (contracts-delta E15).

```text
type: com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled stream: events
type: com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCapacityChanged stream: events
type: com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCancelled stream: events
type: com.retailnetwork.logistics.last-mile-delivery.route.RoutePlanned stream: analytics
type: com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery stream: events
type: com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted stream: events
type: com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageInductionDeferred stream: events
type: com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated stream: events
```

| Event | Subject / key | Raised when | `data` |
|---|---|---|---|
| `DeliveryWaveScheduled` | `waveId` | ScheduleWave is saved (state OPEN) | `{waveId, stationNodeId, serviceDate, inductCutoffAt, windowStart, windowEnd, stopCapacity}` |
| `DeliveryWaveCapacityChanged` | `waveId` | an induction adds a new stop to a wave | `{waveId, stopCapacity, assignedStops, remainingStops}` |
| `DeliveryWaveCancelled` | `waveId` | CancelWave is saved | `{waveId, reason}` |
| `RoutePlanned` | `routeId` | PlanRoute is saved | `{routeId, waveId, dspId, stopCount}` |
| `PackageOutForDelivery` | `routeId` | StartRoute is saved, one per tracking number | `{routeId, trackingNumber, stationNodeId, at}` |
| `DeliveryAttempted` | `routeId` | RecordAttempt is saved, one per package | `{routeId, trackingNumber, outcome, failureReason?, at}` |
| `PackageInductionDeferred` | `trackingNumber` | no wave accepts a package that is at the station | `{trackingNumber, assignmentId?}` |
| `PackageReturnInitiated` | `trackingNumber` | the third failed attempt of a package | `{trackingNumber}` |

Payload rules:

- The `data` field lists are exactly the model's `publishes[].data` lists plus the
  two Gap Register G16 events. Wire types are recorded in `apis/asyncapi.yaml`
  (ADR 0002, assumption A24).
- `failureReason` is present only when `outcome` is `FAILED`; `assignmentId` is
  omitted only when no `LabelIssued` was received for the package.
- `CloseInduction`, `DispatchWave`, `CompleteWave`, `StartRoute` (other than its
  per-package events), `CompleteRoute`, `ContractPartner` and `DeactivatePartner`
  raise no wave, route or partner event.
- A breaking payload change is a new `.v2` type and dataschema, never a mutation
  of v1.

## Consequences

- Consumers need nothing but this catalogue and `apis/asyncapi.yaml`; there is
  no shared Go code.
- Adding a published type requires a line here in the same commit as its
  asyncapi message, or the architecture fitness test fails.
