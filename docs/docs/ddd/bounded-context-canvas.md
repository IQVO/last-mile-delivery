---
id: bounded-context-canvas
title: Bounded context canvas
sidebar_label: Bounded context canvas
sidebar_position: 12
---

# Bounded context canvas

Following the ddd-crew [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas), filled from the code on `develop`.

Source: `docs/adr/0001-last-mile-delivery-bounded-context.md`, `0002`, `0003`, `0004`, `internal/adapters/inbound/*`, `apis/openapi.yaml`, `apis/asyncapi.yaml`.

## 1. Name

Last-Mile Delivery (`last-mile-delivery`), area Network, tier `logistics`.

## 2. Purpose

Runs each delivery station: publishes delivery waves (induct cutoff, delivery window, stop capacity) that the promise relies on, inducts arriving packages into them, plans routes for delivery service partners and records every delivery attempt.

## 3. Strategic classification

| Axis | Value |
| --- | --- |
| Domain | Supporting subdomain ([why](/docs/ddd/subdomain-classification)) |
| Business model | Cost reducer, experience creator |
| Evolution | Custom-built, genesis ([Core domain chart](/docs/ddd/core-domain-chart)) |

## 4. Domain roles

| Role | Fit |
| --- | --- |
| Specification model | `DeliveryWave`: a published specification (cutoff, window, capacity) others plan on |
| Execution context | `Route` and `PackageAttemptHistory`: what actually happened at the doorstep |
| Gateway context | the local read models translate three upstream contexts' vocabularies |
| Audit context | the attempt list and `DeliveryAttempted` events are the record of each try |

## 5. Inbound communication

| Message | Type | From | Collaboration |
| --- | --- | --- | --- |
| `ScheduleWave`, `CloseInduction`, `DispatchWave`, `CancelWave`, `PlanRoute`, `ContractPartner`, `DeactivatePartner` | command (REST) | station planner | UI on Open Host |
| `StartRoute`, `RecordAttempt` | command (REST) | DSP driver app | UI on Open Host |
| list waves, wave stops, route | query (REST, MCP, A2A) | planner, console, agents | Open Host |
| `LoadArrived` | event (Kafka) | `linehaul-transportation` | Customer/Supplier, we are the Customer |
| `NodeRegistered`, `ServiceAreaAssigned` | event (Kafka) | `node-topology` | OHS + PL / Conformist |
| `LabelIssued` | event (Kafka) | `shipping-and-tracking` | OHS + PL / Conformist |

## 6. Outbound communication

| Message | Type | To | Collaboration |
| --- | --- | --- | --- |
| `DeliveryWaveScheduled`, `DeliveryWaveCapacityChanged` | event | `delivery-promise`, `order-sourcing` | OHS + PL / Conformist |
| `PackageOutForDelivery`, `DeliveryAttempted` | event | `shipping-and-tracking` | OHS + PL / Conformist |
| `DeliveryWaveCancelled`, `PackageInductionDeferred`, `PackageReturnInitiated` | event | no named consumer | OHS + PL |
| `RoutePlanned` | event (analytics topic only) | analytics | OHS + PL |

No synchronous call to any context.

## 7. Ubiquitous language

See [Ubiquitous language](/docs/ddd/ubiquitous-language): delivery wave, induct cutoff, delivery window, stop and stop capacity, induction, route, delivery service partner, van capacity, delivery attempt, return to sender.

## 8. Business decisions

1. A package joins the **earliest wave** whose cutoff it arrived before and that has stop capacity left (`InductOnArrival`; `ErrInductionClosed`, `ErrWaveFull`).
2. A **route never exceeds the van's stop capacity** (`ErrRouteOverCapacity`), only uses an active partner of the wave's own station, and only packages inducted in the wave and on no other route.
3. A package is **delivered at most once** (`ErrAlreadyDelivered`); a failed attempt sends it to the next wave; the **third failure returns it to the sender** (`PackageReturnInitiated`).
4. A wave's times are ordered (`ErrInvalidWaveTimes`); an attempt is recorded only on a route out for delivery (`ErrRouteNotStarted`).
5. Capacity is counted in **stops**: packages with one `stopKey` share a stop.
6. Cancelling a wave re-inducts its packages; dispatching re-inducts the packages no route holds; a wave completes with its last route.
7. A wave can only be scheduled and a partner only contracted at a **known delivery station**.

## 9. Assumptions

A1 to A29 in [ADR 0002](/docs/adr/0002-aggregates-and-invariants) (for example A4 stop grouping and the `pkg:` fallback key, A6 earliest eligible wave, A8 station check, A9 wave completes with its last route, A11 dispatch leftovers, A12 route planning rules, A14 one out-for-delivery event per package, A18 and A19 retry and sweep); gap decisions G5, G11, G16, G17, G34 are recorded in the repository's `CLAUDE.md` and the ADRs.

## 10. Verification metrics

| Metric | Where |
| --- | --- |
| `last_mile_delivery.package.attempted` by `outcome` (and `reason`) | counter, [Observability](/docs/operations/observability) |
| HTTP request duration by route | `http.server.request.duration` |
| unpublished outbox rows, parked rows | SQL on `outbox_events` ([Runbook](/docs/operations/runbook)) |
| packages waiting `AT_STATION` | SQL on `package_histories` |

## 11. Open questions

| Question | State in the code |
| --- | --- |
| What does the promise do when a wave is cancelled? | `DeliveryWaveCancelled` is published (G5); no consumer is named in this repository |
| Who consumes `PackageInductionDeferred` and `PackageReturnInitiated`? | none named; they exist for visibility and for a future returns context |
| Is a route re-validated when its partner is later deactivated? | no: only planning checks the partner |
| Route sequencing and optimisation | not modelled: the planner supplies the stops |
| Row cleanup of `idempotency_keys` and published `outbox_events` | no purge job exists |
