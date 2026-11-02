---
id: domain-events
title: Domain events
sidebar_label: Domain events
sidebar_position: 16
---

# Domain events

Every event this context publishes and consumes, as built. The authoritative machine contract is [`apis/asyncapi.yaml`](https://github.com/IQVO/last-mile-delivery/blob/develop/apis/asyncapi.yaml); the architecture test `TestEventCatalogueMatchesContract` pins the catalogue to the code, and the envelope is [ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue). The same list in contract form is on the [Event catalogue](/docs/api-reference/events).

Source: `internal/adapters/outbound/kafka/encoder.go` (payloads, topics), `internal/adapters/kafka/cloudevents/cloudevents.go` (envelope), `internal/domain/{wave,route,history}/events.go` (types and subjects), `internal/adapters/inbound/kafka/consumers.go` (consumed).

## Envelope

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | a UUID minted once per event and stored in the outbox row, so a relay retry carries the same `id` |
| `source` | `/retailnetwork/last-mile-delivery` |
| `type` | `com.retailnetwork.logistics.last-mile-delivery.<entity>.<EventName>`; `<entity>` is `wave`, `route` or `packageattempthistory` |
| `subject` | the aggregate id: `waveId` for wave events, `routeId` for route events, the tracking number for the two package events |
| `time` | the instant the fact happened, UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:last-mile-delivery:<stream>:<EventName>:v1` with `<stream>` = `events` or `analytics` |
| Kafka topic | `retailnetwork.last-mile-delivery.events`; **`RoutePlanned` only** goes to `retailnetwork.last-mile-delivery.analytics` |
| Kafka key | the same value as `subject`, so one aggregate's events stay ordered on one partition |
| Kafka header | `content-type: application/cloudevents+json; charset=UTF-8` (structured mode), plus W3C `traceparent`/`tracestate` when the write ran inside a span |

Every event is written to `outbox_events` in the same transaction as the aggregate and drained by the relay.

## Published

| Event | Full `type` | Stream | Subject / key | Raised by | Known consumers |
| --- | --- | --- | --- | --- | --- |
| `DeliveryWaveScheduled` | `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled` | events | `waveId` | `ScheduleWave` | `delivery-promise`, `order-sourcing` |
| `DeliveryWaveCapacityChanged` | `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCapacityChanged` | events | `waveId` | every induction that takes a **new stop** (`InductOnArrival`, `ScheduleWave` sweep, `RetryNextWave`, re-induction) | `delivery-promise`, `order-sourcing` |
| `DeliveryWaveCancelled` | `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCancelled` | events | `waveId` | `CancelWave` | analytics (G5: an integration event) |
| `RoutePlanned` | `com.retailnetwork.logistics.last-mile-delivery.route.RoutePlanned` | **analytics** | `routeId` | `PlanRoute` | analytics only |
| `PackageOutForDelivery` | `com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery` | events | `routeId` | `StartRoute` (one per tracking number) | `shipping-and-tracking` |
| `DeliveryAttempted` | `com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted` | events | `routeId` | `RecordAttempt` | `shipping-and-tracking` |
| `PackageInductionDeferred` | `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageInductionDeferred` | events | tracking number | a package no wave takes (arrival, un-placement) | none in this repository |
| `PackageReturnInitiated` | `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated` | events | tracking number | the third failed attempt of a package | none in this repository |

"Known consumers" come from the sibling contexts' documentation, not from this repository; no sibling is called synchronously.

### DeliveryWaveScheduled

| Field | Type | Meaning |
| --- | --- | --- |
| `waveId` | string | the wave |
| `stationNodeId` | string | the delivery station (`node-topology` `nodeId`) |
| `serviceDate` | string, `YYYY-MM-DD` | the station's local service date |
| `inductCutoffAt` | date-time | last instant a package can join the wave |
| `windowStart` | date-time | delivery window start |
| `windowEnd` | date-time | delivery window end |
| `stopCapacity` | integer | stops the wave can take |

### DeliveryWaveCapacityChanged

| Field | Type | Meaning |
| --- | --- | --- |
| `waveId` | string | the wave |
| `stopCapacity` | integer | stops the wave can take |
| `assignedStops` | integer | stops already assigned |
| `remainingStops` | integer | `stopCapacity - assignedStops` |

A full snapshot, not a delta: the latest event by `time` wins and a duplicate does no harm. A package joining a stop the wave already holds takes no capacity and raises no event.

### DeliveryWaveCancelled

`waveId` (string) and `reason` (string, 1 to `MaxReason` characters, from the canceller).

### RoutePlanned

`routeId`, `waveId`, `dspId` (strings) and `stopCount` (integer, at most the van capacity). Analytics topic only.

### PackageOutForDelivery

`routeId`, `trackingNumber`, `stationNodeId` (strings) and `at` (date-time, when the route started).

### DeliveryAttempted

`routeId`, `trackingNumber` (strings), `outcome` (`DELIVERED` or `FAILED`), `failureReason` (string, omitted when `DELIVERED`) and `at` (date-time, when the driver says the attempt happened).

### PackageInductionDeferred

`trackingNumber` (string) and `assignmentId` (string, omitted when the label data has not arrived). The package waits in status `AT_STATION` until a wave is scheduled that takes it.

### PackageReturnInitiated

`trackingNumber` (string). The package failed its third attempt and goes back to the sender.

No published event carries an address, a name or any other PII ([ADR 0006](https://iqvo.github.io/warehouse-docs/network/adr/0006-pii-confinement)).

### Example envelope

`DeliveryWaveScheduled` is the fact the promise's last leg rests on. The `id` and `time` are illustrative; the `data` values are the ones the quickstart run produced.

```json
{
  "specversion": "1.0",
  "id": "5b1f0f8e-4c1e-4b8e-9a51-2f3c0d9e7a14",
  "source": "/retailnetwork/last-mile-delivery",
  "type": "com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled",
  "subject": "wave-4587c41f-4d52-49ad-a2bb-6d6199e0c9b6",
  "time": "2026-10-31T22:24:51Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:last-mile-delivery:events:DeliveryWaveScheduled:v1",
  "data": {
    "waveId": "wave-4587c41f-4d52-49ad-a2bb-6d6199e0c9b6",
    "stationNodeId": "node-ds-01",
    "serviceDate": "2026-11-02",
    "inductCutoffAt": "2026-11-02T06:00:00Z",
    "windowStart": "2026-11-02T10:00:00Z",
    "windowEnd": "2026-11-02T18:00:00Z",
    "stopCapacity": 2
  }
}
```

## Consumed

| Producer | Full `type` | Topic | Consumer group variable | What this context does with it |
| --- | --- | --- | --- | --- |
| `linehaul-transportation` | `com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived` | `retailnetwork.linehaul-transportation.events` | `LINEHAUL_CONSUMER_GROUP` | Reads `loadId`, `departureId`, `destinationNodeId`, `nodeType`, `trackingNumbers`, `arrivedAt`. If the node is a `DELIVERY_STATION` (the event's `nodeType`, else `rm_stations`), records the arrivals in `rm_inbound_packages` and runs `InductOnArrival`. Other nodes are ignored. |
| `shipping-and-tracking` | `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued` | `retailnetwork.shipping-and-tracking.events` | `SHIPPING_CONSUMER_GROUP` | Reads `trackingNumber`, `assignmentId`, `stopKey`, `destinationPostalPrefix`; keeps them in `rm_package_stops` (upsert on `trackingNumber`). A label without a usable `stopKey` is ignored. |
| `node-topology` | `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | `retailnetwork.node-topology.events` | `NODE_TOPOLOGY_CONSUMER_GROUP` | Reads `nodeId`, `nodeType`, `regionId`, `timezone`; keeps `DELIVERY_STATION` nodes in `rm_stations`. |
| `node-topology` | `com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned` | `retailnetwork.node-topology.events` | `NODE_TOPOLOGY_CONSUMER_GROUP` | Reads `postalPrefix`, `deliveryStationNodeId`, `regionId`, `assignmentVersion`; applied only if `assignmentVersion` is higher than the stored one. |

Consumers decode only the fields they use (unknown fields are ignored) and dispatch on the **full** `type` string, never on a suffix; any other type on those topics is acknowledged and ignored. Idempotency: the CloudEvents `id` is claimed in `processed_events` in the same transaction as the effect. Failure handling: transient errors are retried up to five times, then the message goes to `<topic>.dlq`; messages that can never be applied are dead-lettered at once ([Runbook](/docs/operations/runbook)).
