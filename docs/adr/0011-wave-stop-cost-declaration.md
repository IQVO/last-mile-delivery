# ADR 0011: wave stop-cost declaration (cents, planner-declared)

## Status

Accepted (2026-11-05).

## Context

The roadmap's feature F7 (money-denominated cost-to-serve) needs a provider of
cost truth for the delivery leg: order-sourcing sums a money-denominated
AssignmentCost per assignment, and one of its inputs is what it costs to run a
stop on a delivery wave. The planner — the actor that already schedules waves
and plans routes — is the only party that can state that cost; this context
owns the wave. The alternatives weighed:

- **Order-sourcing estimates the cost itself.** Rejected: it would duplicate
  planner knowledge it does not have, and two consumers would drift apart.
- **A per-package cost.** Rejected: the wave's capacity and its routes are
  counted in stops (packages share a stopKey, Gap Register G11), so the natural
  marginal unit of a wave is the stop, not the package.
- **A decimal amount with a currency field.** Rejected for now: this is a
  single-currency study; integer cents keep the arithmetic exact and the
  contract free of a currency nobody would read.

## Decision

1. The DeliveryWave aggregate carries `costPerStopCents`, an `int64` in integer
   cents, default 0. The command `DeclareCost(waveID, costPerStopCents, now)`
   sets it, bumps the version and raises `WaveCostDeclared`
   (`{waveId, costPerStopCents, declaredAt}`) on
   `retailnetwork.last-mile-delivery.events` (subject and key = waveId,
   dataschema `urn:retailnetwork:last-mile-delivery:events:WaveCostDeclared:v1`).
2. The declared value is the planner's **marginal cost of ONE delivery stop**
   on that wave — route driver time plus the stop's mileage share — per stop
   (the G11 stopKey grouping), never per package. Valid range 0 to 10000000
   cents; there is no currency field (single-currency study).
3. `DeclareCost` is allowed while the wave still runs (OPEN, INDUCTION_CLOSED
   or DISPATCHED) and refused with `ErrIllegalTransition` once the wave is
   CANCELLED or COMPLETED. A later declaration overwrites the earlier one and
   publishes again; the last one wins.
4. **0 versus undeclared.** 0 is a legal declaration and means a declared zero
   marginal cost. A wave with no declaration reads 0 and contributes 0 to a
   consumer's sum, but the consumer's estimate stays incomplete — whether an
   undeclared wave may be treated as free is the consumer's decision, not this
   context's (ADR 0003 keeps consumed-contract reading rules with consumers).
5. REST: `POST /waves/{waveId}/cost-declarations` (noun-transition route like
   `/cancellations`), Idempotency-Key required, 201 returning the wave; every
   wave body carries `costPerStopCents`.
6. order-sourcing consumes `WaveCostDeclared` into its money-denominated
   AssignmentCost (roadmap F7); it already consumes this topic for the wave
   schedule events.

## Consequences

- The wave table gains one column (`cost_per_stop_cents`, migration 0003);
  rows written before it restore 0, which reads as undeclared.
- The event count on the integration topic rises from seven to eight; the
  catalogue (ADR 0004) and `apis/asyncapi.yaml` moved in the same commit.
- A currency field becomes necessary the day a second currency enters the
  study; that is a new dataschema version, not an edit of this one.
- What the planner bases the number on (driver pay, fuel, van depreciation)
  stays outside the contract; only the cents cross the boundary.
