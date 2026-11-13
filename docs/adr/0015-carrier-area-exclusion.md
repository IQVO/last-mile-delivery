# ADR 0015: carrier-area exclusion (the last mile refuses CARRIER_HANDOFF areas)

## Status

Accepted (2026-11-13)

## Context

The retail network is growing a second delivery mode: a planner may declare a
service area `CARRIER_HANDOFF` in node-topology, meaning the final leg of that
area's packages is carried by a contracted carrier from a handoff node instead
of a station's own vans (node-topology ADR 0016, roadmap C4). The mode rides
the existing `ServiceAreaAssigned` event additively: `deliveryMode` is omitted
for `NETWORK_ROUTE`, which is the current behaviour of every area today.

Last-mile delivery owns the station waves, the routes and the induction policy.
A carrier-area package must never enter a station wave: it does not ride a van,
so inducting it would consume stop capacity, raise `PackageInducted`-path
history and wait forever for a route that will not run. The promise for such a
package is computed against the chosen carrier option by delivery-promise
(its ADR 0026), and the physical handoff is recorded by shipping-and-tracking
(its ADR 0015). Nothing in that chain needs this service.

Today the induction path (`arrive` -> `arriveNew` -> `stopInfo` -> `place`)
performs no prefix-versus-area check at all: every package that reaches a
delivery station is inducted into the earliest eligible OPEN wave (A6, A18).

## Decision

This service keeps consuming `ServiceAreaAssigned` on the same full type
string and stores the declared delivery mode in its area read model; the
induction path refuses packages whose destination area is `CARRIER_HANDOFF`.

The contract (verbatim from the wave-3 design, section 4.3):

- `views.ServiceArea` gains `DeliveryMode` (`LM:internal/application/usecases/consumers.go` `ServiceAreaAssignedCommand` + decode struct in `LM:internal/adapters/inbound/kafka/consumers.go`); migration `0006_area_delivery_mode` adds `delivery_mode TEXT NOT NULL DEFAULT 'NETWORK_ROUTE'` to the service-area read-model table; memory twin + repocontract.
- `arriveNew` (and `retryWaiting`'s caller path) refuse carrier areas: look up the package's prefix (`stopInfo`'s sibling: the PackageStopView already carries `DestinationPostalPrefix`, F17), load the area, and when the mode is CARRIER_HANDOFF record a metric (`last_mile_delivery.carrier_area.skipped`) and return NO events and NO history row — the package never enters LM. The station's waves for NETWORK_ROUTE prefixes are untouched. Failure mode: an area flipped to CARRIER_HANDOFF after induction keeps already-inducted packages on their wave (documented, harmless: one wave, then the new mode applies).

Decoding is tolerant, as everywhere: an event without `deliveryMode` (an old
producer or an old replay) decodes to `NETWORK_ROUTE`, which changes nothing.
The mode is not a wire contract of this service: no new event is published and
no consumed type is added; the only externally visible change is the counter
below and the absence of induction.

The metric `last_mile_delivery.carrier_area.skipped` (Tier 2 of the fleet
metrics convention, ADR 0008) counts every refused package, so a rising rate
is visible when an area flips and traffic silently stops inducting.

## Consequences

- A package whose `LabelIssued` prefix has no area row, or whose label data is
  missing (the `pkg:<trackingNumber>` fallback stop), is inducted as today:
  refusal needs a known `CARRIER_HANDOFF` assignment.
- An area flipped to `CARRIER_HANDOFF` after induction keeps the
  already-inducted packages on their wave — one wave, then the new mode
  applies. A package that was deferred (waiting at the station) when the area
  flipped stops being retried: `retryWaiting` refuses it too, it stays
  AT_STATION and is eventually handled by the station's return process, like
  any other package the waves cannot take.
- The station's waves for `NETWORK_ROUTE` prefixes are untouched.
- Rollback is additive: the column stays, and reassigning the area to
  `NETWORK_ROUTE` (version+1) restores induction on the next arrival.
- No deploy change: no new topic, subscription, env var or chart value. The
  feature is dormant until node-topology declares the first `CARRIER_HANDOFF`
  area.
