# ADR 0016: wave stop-emission declaration (gCo2e, planner-declared)

## Status

Accepted (2026-11-14).

## Context

The roadmap's feature C9 (carbon per shipment) needs a provider of emission
truth for the delivery leg: order-sourcing records an `estimatedCo2eGrams`
beside its money-denominated estimate, and one of its inputs is the greenhouse
cost of running a stop on a delivery wave, in grams of CO2 equivalent. The
planner — the actor that already schedules waves and plans routes — is the only
party that can state that figure; this context owns the wave. The alternatives
weighed:

- **Order-sourcing estimates the emission itself.** Rejected: it would
  duplicate planner knowledge it does not have, and two consumers would drift
  apart.
- **A per-package emission.** Rejected: the wave's capacity and its routes are
  counted in stops (packages share a stopKey, Gap Register G11), so the natural
  marginal unit of a wave is the stop, not the package.
- **A decimal amount with a unit field.** Rejected for now: one integer gram
  figure keeps the arithmetic exact and the contract free of a unit nobody
  would read; grams of CO2 equivalent (gCo2e) is the declared unit.

## Decision

1. The DeliveryWave aggregate carries `emissionPerStopG`, an `int64` in grams
   of CO2 equivalent (gCo2e), default 0. The command
   `DeclareEmission(waveID, gCo2ePerStop, now)` sets it, bumps the version and
   raises `WaveEmissionDeclared` (`{waveId, gCo2ePerStop, declaredAt}`) on
   `retailnetwork.last-mile-delivery.events` (subject and key = waveId,
   dataschema `urn:retailnetwork:last-mile-delivery:events:WaveEmissionDeclared:v1`).
2. The declared value is the planner's **marginal greenhouse emission of ONE
   delivery stop** on that wave — the stop's share of the route's driving —
   per stop (the G11 stopKey grouping), never per package. Valid range 0 to
   10000000 gCo2e; there is no unit field (gCo2e is the declared unit).
3. `DeclareEmission` is allowed while the wave still runs (OPEN,
   INDUCTION_CLOSED or DISPATCHED) and refused with `ErrIllegalTransition`
   once the wave is CANCELLED or COMPLETED. A later declaration overwrites
   the earlier one and publishes again; the last one wins.
4. **0 versus undeclared.** 0 is a legal declaration and means a declared zero
   marginal emission. A wave with no declaration reads 0 and contributes 0 to
   a consumer's sum, but the consumer's estimate stays incomplete — whether an
   undeclared wave may be treated as emission-free is the consumer's decision,
   not this context's (ADR 0003 keeps consumed-contract reading rules with
   consumers).
5. REST: `POST /waves/{waveId}/emission-declarations` (noun-transition route
   like `/cancellations`), Idempotency-Key required, 201 returning the wave;
   every wave body carries `gCo2ePerStop`.
6. order-sourcing consumes `WaveEmissionDeclared` into its
   `estimatedCo2eGrams` (roadmap C9); it already consumes this topic for the
   wave schedule and stop-cost events. Ranking is untouched by this fact:
   carbon is recorded, not optimised, in this slice.

## Consequences

- The wave table gains one column (`emission_per_stop_g`, migration 0007);
  rows written before it restore 0, which reads as undeclared.
- The event count on the integration topic rises from ten to eleven; the
  catalogue (ADR 0004) and `apis/asyncapi.yaml` moved in the same commit.
- What the planner bases the number on (fleet mix, fuel, load factor) stays
  outside the contract; only the grams cross the boundary.
