# ADR 0017: return-pickup inductions (Induction.Kind, COLLECTED, PackagePickupOut)

## Status

Accepted (2026-11-14).

## Context

The network's return authorization (the customer-returns context) can offer a
shopper a doorstep **pickup** as the return method instead of a drop-off: the
reverse label's package is collected at the same stop the outbound package was
delivered to, by the same last-mile operation that delivers. Until now every
package this context inducts is a delivery: outcomes are `DELIVERED` or
`FAILED` only, and `Route.Start` announces every package as
`PackageOutForDelivery`.

The reverse label carries the same ship-to, so its stopKey equals the outbound
stopKey by construction: a pickup stop groups with the delivery stop at the
same address for free. What is missing is the *reason* a package takes a stop
(deliver to it, or collect from it), an outcome for a successful collection,
and an event that tells downstream contexts a package is out for pickup, not
out for delivery.

The alternatives weighed:

- **A separate pickup aggregate or context.** Rejected: a pickup stop IS a
  stop — it shares the wave's capacity, the route's van and the attempt
  history; a second model would duplicate all of it.
- **Reusing `PackageOutForDelivery` with a payload flag.** Rejected: the
  outbound scan flow of shipping-and-tracking keys on the event type; a
  pickup announced as out-for-delivery would poison that read model. A new
  type keeps consumers dispatching on the full type string, and the old one
  untouched.
- **A `PICKED_UP`-style attempt outcome only, no induction kind.** Rejected:
  `COLLECTED` must be legal exactly for packages inducted as pickups, and
  only the induction can carry that fact.

## Decision

1. `Induction` gains `Kind`, `DELIVERY` (the default) or `PICKUP`. The command
   is `Induct(trackingNumber, stopKey, kind, at, now)`; an unknown kind is a
   value-object error. A `PICKUP` induction occupies stop capacity, follows
   the uniqueness rules and raises `DeliveryWaveCapacityChanged` exactly like
   any other induction. The persisted form adds `kind` (migration 0008,
   `wave_inductions.kind TEXT NOT NULL DEFAULT 'DELIVERY'` with a CHECK of
   the two values); an omitted kind decodes as `DELIVERY`.
2. **Activation-gated, dormant by default:** no writer of this context passes
   `PICKUP` until the return authorization flow requests it. Every existing
   writer — the arrival, retry, sweep and re-induct paths — passes
   `DELIVERY`, so the default is what every wave holds until then. This
   context has NO REST induction route to extend (induction is driven by the
   `LoadArrived` consumer; ADR 0003): the kind rides the domain seam above,
   and the wave's check (not a route) is its only gate.
3. `DeliveryAttempted.outcome` gains `COLLECTED`. It is valid only when the
   package's induction is `PICKUP`; `FAILED` stays valid for both kinds;
   `DELIVERED` stays valid as before. `checkAttempt` is extended: a
   `COLLECTED` attempt carries no `failureReason` and no `outcomeDetail`
   (none of the detail categories fits a collection). On the
   PackageAttemptHistory a `COLLECTED` attempt is a terminal success — it
   takes the same `RecordDelivered` transition and the four statuses are
   unchanged.
4. `Route.Start` raises `PackageOutForDelivery` for `DELIVERY` packages and a
   new `PackagePickupOut` `{routeId, trackingNumber, stopKey, stationNodeId,
   at}` for `PICKUP` packages — type
   `com.retailnetwork.logistics.last-mile-delivery.route.PackagePickupOut`,
   same topic, subject and Kafka key as `PackageOutForDelivery` (the routeId),
   so both streams stay ordered per route. The analytics projector skips it
   committed-past (no report needs it; ADR 0012).
5. A third `FAILED` attempt of a pickup package raises the existing
   `PackageReturnInitiated` (G16) unchanged; the return context consumes it
   as its pickup-failed fact. Nothing in this context changes for that path.

## Consequences

- The catalogue (ADR 0004) and `apis/asyncapi.yaml` gain the
  `PackagePickupOut` type and payload; the REST attempt outcome enum
  (`apis/openapi.yaml`) gains `COLLECTED`. Both stay additive and optional:
  consumers that decode tolerantly see nothing new until a pickup is
  inducted, and no pickup is until the return flow asks for one.
- The derived per-stop outcome (G17) is unchanged: `COLLECTED` is not
  `FAILED`, so a stop of collected packages derives `DELIVERED`; the stop
  view keeps its two values.
- Rollback is the down migration `0008`; no event or row written before
  activation mentions a pickup.
