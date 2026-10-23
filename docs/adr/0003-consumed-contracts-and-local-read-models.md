# ADR 0003: consumed contracts, local read models and relationships

## Status

Accepted (2026-10-23).

## Context

last-mile-delivery consumes four events and makes no synchronous call into a
sibling context (model `sync_calls: []`, common rule 4). What it needs from a
neighbour is a local, event-fed read model.

## Decision

### Consumed events

| Event (full type) | Producer | Topic | Read model | Used by |
|---|---|---|---|---|
| `com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived` | linehaul-transportation | `retailnetwork.linehaul-transportation.events` | `rm_inbound_packages` (InboundPackagesView) | policy InductOnArrival |
| `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | node-topology | `retailnetwork.node-topology.events` | `rm_stations` (StationView) | ScheduleWave, ContractPartner, LoadArrived (A8) |
| `com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned` | node-topology | `retailnetwork.node-topology.events` | `rm_station_service_areas` (StationView) | none yet (A28) |
| `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued` | shipping-and-tracking | `retailnetwork.shipping-and-tracking.events` | `rm_package_stops` (PackageStopView) | stop grouping (G11), assignmentId (A20) |

`LabelIssued` is the addition of contracts-delta E10 (stopKey and
destinationPostalPrefix on the label event); `LoadArrived` carries `nodeType`
since E3. The producers' AsyncAPI files are the authority: node-topology's was
read from `origin/feature/service`; linehaul-transportation's and
shipping-and-tracking's were not pushed when this was written, so their inbound
contracts were built from the model fields, contracts-delta and the wire
conventions, and are vendored here (`apis/asyncapi.yaml`, `receive` operations)
as tolerant copies of the fields used. They are to be re-checked against the
producers' files once pushed.

### Consumption rules

- One consumer per topic, one consumer group per consumer, each a mandatory
  environment variable (`LINEHAUL_CONSUMER_GROUP`, `NODE_TOPOLOGY_CONSUMER_GROUP`,
  `SHIPPING_CONSUMER_GROUP`); a missing one is a boot error naming it.
- The CloudEvents `id` is claimed in `processed_events` (the inbox) in the SAME
  transaction as the effect; a redelivered event is applied once.
- Dispatch is on the full `type` string; an unknown type and unknown fields are
  ignored; a message that is not a CloudEvents 1.0 event, or whose `data` cannot
  be decoded, goes to the dead-letter topic (`<topic>.dlq`) at once and the offset
  is committed; a transient failure is retried five times before the same fate.
- A deterministic skip (a `LoadArrived` whose `nodeType` is not `DELIVERY_STATION`,
  or a decodable event with no usable key) is not retried: the event is applied
  as "ignored" and marked processed (ADR 0002 A8: the event's `nodeType` wins; the
  `StationView` is consulted only when the event carries none). A transient failure
  (database, concurrent update after the retry) is returned so the message is
  redelivered.
- `NodeRegistered` keeps only `DELIVERY_STATION` nodes (first write wins; a node
  type never changes). `ServiceAreaAssigned` is applied only when its
  `assignmentVersion` is higher than the stored one for the prefix.
  `LabelIssued` keeps the first values per tracking number; a label without
  `stopKey` is ignored (A4).
- `LoadArrived` records every tracking number in `rm_inbound_packages` and inducts
  it under ADR 0002 A6 to A8, A18 and A19.

### Relationships (model `inbound`)

- linehaul-transportation: Customer/Supplier (upstream, we conform to its fields
  and ask for additions through the orchestrator).
- node-topology: Conformist.
- shipping-and-tracking: upstream for `LabelIssued` (Customer/Supplier), downstream
  of us for `PackageOutForDelivery`, `DeliveryAttempted` and
  `PackageReturnInitiated`.
- delivery-promise and order-sourcing: downstream of the wave events; the
  published contract is `apis/asyncapi.yaml` and ADR 0004.
- station planner and DSP driver app: UI over REST (`apis/openapi.yaml`).

### Publishing

Every published event leaves through a transactional outbox (`outbox_events`):
the aggregates and the encoded CloudEvents rows commit in one transaction and an
always-on relay writes them to Kafka (ADR 0004).

## Consequences

- A station that was never registered cannot receive waves or partners. A
  `LoadArrived` for it that carries `nodeType: DELIVERY_STATION` still inducts or
  defers its packages (the `PackageInductionDeferred` risk signal is kept); one
  with no `nodeType` is ignored until the `NodeRegistered` is consumed.
- The `rm_` tables lag their producers; no aggregate holds a foreign key to them.
