---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/outbox/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

last-mile-delivery PUBLISHES eight events: seven to
`retailnetwork.last-mile-delivery.events` and `RoutePlanned` ONLY to
`retailnetwork.last-mile-delivery.analytics`; it CONSUMES four (ADR 0003). Never
produce or consume a `warehouse.*` topic.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces is a CloudEvents 1.0 event in
structured content mode. This is a hard rule, not a preference:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write, no
  envelope toggle env var. `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build and validate the envelope with `github.com/cloudevents/sdk-go/v2/event`
  through ONE helper package, `internal/adapters/kafka/cloudevents` (`New`,
  `Decode`, `ContentTypeHeader`). Transport stays `segmentio/kafka-go`; no
  hand-rolled CloudEvent structs. The only caller is the encoder
  `internal/adapters/outbound/kafka/encoder.go`.
- Every message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`. Message key =
  subject = aggregate id, `kafkago.Hash{}` balancer
  (`internal/adapters/outbound/kafka/writer.go`).
- Attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per event at outbox
  append and stored in `outbox_events.event_id`, so a relay retry carries the same
  id); `source=/retailnetwork/last-mile-delivery`; `type`; `subject` (the aggregate
  id: waveId, routeId or trackingNumber; never empty); `time` (the domain
  occurred-at, UTC); `datacontenttype=application/json`;
  `dataschema=urn:retailnetwork:last-mile-delivery:<events|analytics>:<EventName>:v1`.
  No custom extension attributes without an ADR.
- `type` = `com.retailnetwork.logistics.last-mile-delivery.<entity>.<EventName>` with
  entity `wave`, `route` or `packageattempthistory` (lowercase, no separators). A
  breaking payload change is a new `.v2` dataschema, never a mutation of v1;
  evolution is additive only.
- Payloads follow the wire conventions of contracts-delta: ids are strings, instants
  RFC 3339 UTC, enums UPPER_SNAKE, optional fields omitted, arrays always present.
  No payload carries an address or any PII.
- Tests: a byte-exact golden per published type
  (`internal/adapters/outbound/kafka/encoder_test.go`), the catalogue-vs-contract
  sensor (`TestEventCatalogueMatchesContract`), and the Kafka proofs against a real
  broker (`cmd/api/e2e_integration_test.go`, `wiring_integration_test.go`), all via
  testcontainers.

### Published types

`dataschema` is `urn:retailnetwork:last-mile-delivery:<stream>:<Event>:v1`.

| `type` | stream | `subject` |
| --- | --- | --- |
| `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled` | events | waveId |
| `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCapacityChanged` | events | waveId |
| `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCancelled` | events | waveId |
| `com.retailnetwork.logistics.last-mile-delivery.route.RoutePlanned` | analytics | routeId |
| `com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery` | events | routeId |
| `com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted` | events | routeId |
| `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageInductionDeferred` | events | trackingNumber |
| `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated` | events | trackingNumber |

### Consumed types

One consumer per topic, in `internal/adapters/inbound/kafka`; dispatch is on the
FULL `type`; unknown types and unknown fields are ignored.

| `type` | topic | read model |
| --- | --- | --- |
| `com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived` | `retailnetwork.linehaul-transportation.events` | `rm_inbound_packages`, induction |
| `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | `retailnetwork.node-topology.events` | `rm_stations` |
| `com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned` | `retailnetwork.node-topology.events` | `rm_station_service_areas` |
| `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued` | `retailnetwork.shipping-and-tracking.events` | `rm_package_stops` |

The CloudEvents `id` is claimed in `processed_events` in the SAME transaction as
the effect (`ports.Inbox`); a message that is not a CloudEvent or whose `data`
cannot be decoded goes to `<topic>.dlq` at once, a transient failure after five
attempts (`consumer.go`, `deadletter.go`).

## Transactional outbox

A use case saves the aggregates and appends the encoded events in ONE Postgres
transaction (`ports.EventOutbox`, `internal/adapters/outbound/postgres/outbox.go`).
The relay (`internal/adapters/outbound/outbox/relay.go`, always on, started by
`cmd/api/main.go` `startOutboxRelay`) drains `outbox_events` in id order,
at-least-once. The cap `OUTBOX_MAX_ATTEMPTS` (default 20, 0 = unlimited) excludes a
poison row; re-arm it with `UPDATE outbox_events SET attempts = 0 WHERE id = ...`.
The first write to a topic that does not exist yet can fail once with an
unknown-topic error: the relay retries it with the same id. The caller of
`NewRelay` must pass `WithMaxAttempts(...)` inline (`TestOutboxRelaysPassAttemptCap`).

## Consumer group ids

Each consumer's group id comes from a mandatory env var (`LINEHAUL_CONSUMER_GROUP`,
`NODE_TOPOLOGY_CONSUMER_GROUP`, `SHIPPING_CONSUMER_GROUP`): a boot error naming it,
never a string literal (`TestKafkaConsumerGroupNeverHardcodedInline`). The inbox
table and dispatch rules of `.claude/rules/fleet/kafka-testing-and-consumers.md` apply.
