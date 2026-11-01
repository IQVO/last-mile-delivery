---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
sidebar_position: 2
---

# Subdomain classification

**Last-Mile Delivery is a Supporting subdomain** of the retail network (area Network, tier `logistics`). The class is the fleet's verdict in [warehouse-docs, Subdomain Classification](https://iqvo.github.io/warehouse-docs/network/strategic-design/subdomain-classification) (`network/docs/strategic-design/subdomain-classification.md` on `main`); this page gives the reasoning from the code and the neighbours' classes copied from that page.

| Field | Value |
| --- | --- |
| Problem-space subdomain | Last-Mile Delivery: "Getting packages from a delivery station to the door" (Supporting) |
| Bounded context | `last-mile-delivery` |
| Classification | Supporting |
| Tier | `logistics` |
| Business model | Cost reducer, experience creator |
| Evolution | Custom-built |
| Why (warehouse-docs) | "Waves with stop capacity are what the promise's last leg stands on; routing itself is replaceable." |

## Reasoning

**Why not Core.** The context does not decide what the network promises or how an order is sourced; `delivery-promise` and `order-sourcing` do, and they are Core. This context supplies one published fact to them, the **delivery wave** (induct cutoff, delivery window, stop capacity), and does the station-level work behind it. The work itself is rule-bound and replaceable: a package joins the earliest wave that still has a stop free; a route may not have more stops than the van holds; a package is delivered once; three failures send it back. There is no optimisation in the code: `Route` is planned by the caller (the station planner sends the stops), not computed (`route.Plan` only checks the plan), and induction is "first eligible wave in cutoff order" (`internal/application/usecases/induct.go`, `place`).

**Why not Generic.** Waves counted in **stops** (distinct stop keys, not packages), the stop-grouping by `stopKey`, the re-induction on cancel and dispatch, and the three-failure return are specific to this network's model, and the promise depends on the exact semantics of the wave capacity events. It cannot be bought off the shelf without conforming to someone else's wave model.

**Why Supporting.** Every promise's last leg stands on a wave, so the context must be correct and available, but nobody chooses a network for how it keeps its station's waves. It is custom-built because the wave semantics are the network's own.

**Evidence from the code.**

- Four small aggregates (`DeliveryWave`, `Route`, `DeliveryServicePartner`, `PackageAttemptHistory`), each a few invariants and a short state machine (`internal/domain/{wave,route,partner,history}`).
- No cross-aggregate rule in the domain; cross-aggregate behaviour is application policy (`InductOnArrival`, `InductDeferredOnSchedule`, `RetryNextWave`, `CompleteWaveOnLastRoute`) in `internal/application/usecases`.
- Three consumers that only keep local read models fresh (`rm_stations`, `rm_station_service_areas`, `rm_package_stops`, `rm_inbound_packages`).
- Mutation efficacy 100 % over the five domain packages ([Testing](/docs/development/testing)).

## Neighbours

Classes copied from the same warehouse-docs page:

| Context | Classification | Tier | Business model | Relation to this context |
| --- | --- | --- | --- | --- |
| `delivery-promise` | Core | `commerce` | Revenue (conversion) and cost (no promise the network cannot keep) | consumes the wave events to quote the last leg |
| `order-sourcing` | Core | `commerce` | Cost-to-serve reduction, speed protection | consumes the wave events to choose a real path |
| `node-topology` | Supporting | `logistics` | Enabler | upstream: `NodeRegistered`, `ServiceAreaAssigned` |
| `linehaul-transportation` | Supporting | `logistics` | Cost reducer, speed enabler | upstream: `LoadArrived` |
| `shipping-and-tracking` | Generic | `logistics` | Compliance and experience | upstream for `LabelIssued`; downstream of this context's out-for-delivery and attempt events |

Details of each edge are in [Integration](/docs/ecosystem/integration) and the [Context map](/docs/ddd/context-map).

## What would move it

A routing or sequencing engine in this context (computing stop order, balancing vans, re-planning in flight) that the promise relied on for its date would push it towards Core; replacing the wave bookkeeping with a bought dispatch product behind an anti-corruption layer would push it to Generic. Neither exists today: the model states routing is replaceable.
