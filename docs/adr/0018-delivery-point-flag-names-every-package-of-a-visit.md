# ADR 0018: the delivery-point flag names every package of a failed visit (refines ADR 0014)

## Status

Accepted (2026-11-18). Refines [ADR 0014](./0014-delivery-point-profile.md); it does not
supersede it, and the body of ADR 0014 is unchanged. Where the two differ on how a
strike names packages and on how many events one observation raises, this ADR wins.

## Context

ADR 0014 says a `DeliveryPointFlagged` carries "the newly affected packages only", the
packages a strike reaches that were not told yet. The code did not honour that for a
visit of more than one package. A strike held ONE tracking number: the first counted
failure of a route at the stop. A second package of the same order (or of any order
going to the same stop) that failed on the same route found a strike of its route
already there, added nothing, and was never named: `DeliveryPointFlagged.trackingNumbers`
carried the first package of each visit only. Downstream, shipping-and-tracking relays
the flag per tracking number (`PackageDeliveryPointFlagged`) and customer-ordering shows
the advisory per package, so the second package of a failed visit never got the
delivery-point advisory, even though it failed at the same flagged point at the same time.

Facts the fix rests on:

- `trackingNumbers` is already an array with `minItems: 1`, `maxItems: 5` in
  `apis/asyncapi.yaml` (`DeliveryPointFlaggedEvent`); the field and the type do not
  change, only how many items the producer fills in.
- Attempts reach the profile one package at a time (`RecordAttempt` calls `Observe`
  once per package), so the packages of one visit arrive as separate observations
  seconds or minutes apart.
- ADR 0014 counts strikes per route: two parcels failing at one stop on one route are
  ONE strike. That stays: it is the flag threshold.

Alternatives weighed: (a) raise one strike per package: rejected, it changes the
threshold semantics of ADR 0014 (two parcels of one visit would flag the point at once);
(b) keep the strike as it was and tell the packages of the route from the route
aggregate: rejected, the profile would read another aggregate's data and the use case
would need a second lookup per attempt; (c) name every package in the strike and tell
each package once: chosen.

## Decision

1. **A strike names every package of its visit that failed at the stop.** The first
   counted failure of a route at the stop opens the strike, as before. A later counted
   failure of the same route at the same stop joins that strike (its tracking number is
   appended, in report order, once). Only counted failures join (`NOT_HOME`,
   `ACCESS_PROBLEM`); `OTHER`, a failure without a detail and a delivery never do. A strike
   names 1 to `MaxStrikePackages = 50` packages; a 51st package of one visit is tallied
   by the counters but not named (a stop holding that many parcels is not expected, the
   bound only keeps a stored row small).
2. **`untold` is a set.** A package is *told* when a `DeliveryPointFlagged` carried its
   tracking number; each strike keeps its told packages. The packages to tell are the
   distinct tracking numbers of the open strikes that no open strike records as told, in
   strike order then report order. After an observation every strike that names a told
   package records it as told, so a package already flagged is never flagged again while
   any strike of it stays open, even when its first strike leaves the ring or the window.
3. **A later visit adds only the newly affected packages.** Each observation raises the
   events for the packages it made untold, whatever visit they belong to. A package of
   the visit that failed after the flag opened is told by its own event; a package told
   before is not repeated.
4. **The event is unchanged in type, shape and `dataschema`.** `trackingNumbers` holds
   the packages newly affected by the observation, at most `MaxEventPackages = 5` (the
   contract's `maxItems`). When more than five are new at once (a visit of six or more
   packages joining a point flagged by the observation) the packages are split in order
   over several events of the same type, each with the same `stopKey`, `reason`,
   `failures`, `flaggedAt` and header version, so no package is dropped. A consumer
   already handles repeated flags per tracking number (ADR 0014: idempotent per tracking
   number).
5. **Unchanged from ADR 0014:** `FlagAfterStrikes = 2`, `MaxStrikes = 5` (the ring),
   the 60-day window measured from the newest attempt time, one strike per route and
   visit, a delivery on a route with no strike ends the episode, `reason` is
   `ACCESS_PROBLEM` when any open strike has it and the strike's category is the one of
   the first counted failure of its visit. `StrikeWindow = 60 * 24h` is now
   `StrikeWindowDays = 60` (cutoff `ref.AddDate(0, 0, -60)` in UTC, the same instant),
   so the package passes the mutation gate with no constant-expression survivor.
6. **Persistence.** The `strikes` jsonb element gains `trackingNumbers` (array) and
   `told` (array). `trackingNumber` (the first package) and `notified` (every package
   told) are still written, so the previous release can read a row this release wrote;
   a row written by the previous release (no `trackingNumbers`) decodes as a strike of
   that one package, told when `notified` was true. No migration, no backfill.
7. **No new endpoint, topic, type, env var or migration.** `Restore` additionally
   validates that a strike names 1 to `MaxStrikePackages` distinct valid tracking
   numbers and that `told` is a distinct subset of them.

## Consequences

- **Visible behaviour change.** The second and later packages that fail at a flagged
  point in one visit are now told. Each one raises its own `DeliveryPointFlagged` (one
  per observation that made packages untold), so a visit of N packages at a flagged
  point can raise up to N events instead of one, and shipping-and-tracking and
  customer-ordering show the advisory on every affected package. A package that fails
  alone behaves exactly as before.
- **Rollout is safe in any order.** The event type, the topic, the key, the `dataschema`
  (`...:DeliveryPointFlagged:v1`) and the payload shape are unchanged; only more array
  items (still at most five) appear, which the contract already allows. No consumer or
  pinned contract copy needs a refresh for shape; `apis/asyncapi.yaml` changes
  description text only.
- Strikes recorded before the deploy hold one package each; the packages that failed
  later in those visits and were never named stay untold (no backfill: the profile does
  not keep them). Visits recorded after the deploy are complete.
- The row of a profile is larger by at most 50 tracking numbers per strike (ring of 5).
- A visit whose packages fail with different counted categories keeps the category of
  its first failure; `reason` is therefore `ACCESS_PROBLEM` only when the first counted
  failure of some open visit had that category. Taking the strongest category of a visit
  is a product decision left open.
- No ADR 0004 catalogue change: the type is already listed and no type is added.
