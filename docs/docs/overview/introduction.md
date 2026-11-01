---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
---

# Last-Mile Delivery

`last-mile-delivery` is the bounded context that **runs each delivery station of the retail network**. A delivery station is a network-operated node (see [warehouse-docs ADR 0004](https://iqvo.github.io/warehouse-docs/network/adr/0004-parcel-nodes-are-network-operated)). The context publishes **delivery waves**: dispatch slots with an induct cutoff, a delivery window and a stop capacity, which `delivery-promise` and `order-sourcing` plan the last leg of every path on. When a load arrives at a station it **inducts** each package into the earliest wave that still has room, plans **routes** for independent **delivery service partners** (DSPs) within each van's stop capacity, and records every **delivery attempt** as `DELIVERED` or `FAILED`. A failed package goes into the next wave; after three failures it goes back to the sender.

| | |
| --- | --- |
| Area | Network (retail network, tier `logistics`) |
| Subdomain class | Supporting ([reasoning](/docs/ddd/subdomain-classification)) |
| Language / runtime | Go 1.27, hexagonal, real PostgreSQL and Kafka only |
| Binaries | `cmd/api` (REST, three Kafka consumers, outbox relay), `cmd/mcp` (read-only MCP), `cmd/a2a` (read-only A2A agent) |
| Console | `web/`, the Module Federation remote `last_mile_mfe` |
| Repository | [IQVO/last-mile-delivery](https://github.com/IQVO/last-mile-delivery) |

## The business problem

A promise is only as good as its last leg. `delivery-promise` quotes a delivery date only when it can show a witness path that ends in a **delivery wave** at the destination station; `order-sourcing` chooses the real path the same way. Both need to know, ahead of time and without a call, three things about every station: when a package must be at the station to make a wave (the **induct cutoff**), when that wave delivers (the **delivery window**), and how many more stops it can take (its **stop capacity**). This context owns those facts and keeps them true, and does the work behind them.

It is a context of its own because the last mile has a different clock and capacity unit from the middle mile (it counts **stops**, in waves and in vans, not packages on departures), because DSPs are independent businesses with their own vans, and because the promise needs a stable published fact it can consume as an event-fed read model.

## What it owns

| Aggregate | Owns |
| --- | --- |
| `DeliveryWave` | one dispatch wave of one station on one service date: cutoff, window, stop capacity, the inducted packages grouped into stops; states `OPEN`, `INDUCTION_CLOSED`, `DISPATCHED`, `COMPLETED`, `CANCELLED` |
| `Route` | one partner's van route within a wave: ordered stops, attempts; states `PLANNED`, `OUT_FOR_DELIVERY`, `COMPLETED` |
| `DeliveryServicePartner` | an independent delivery business contracted to a station, its van capacity; `ACTIVE` or `INACTIVE` |
| `PackageAttemptHistory` | per tracking number: where the package is in its last-mile life and how many attempts failed (at most 3) |

The business decisions it makes: a package joins the **earliest wave** it arrived before that has stop capacity left; a **route never exceeds its van's stop capacity**; a package is **delivered at most once**, and a failed one returns to the station for the next wave, until the third failure returns it to the sender; a wave's times are ordered; an attempt is recorded only on a route that is out for delivery.

## What it does not own

| Out of scope | Where it lives |
| --- | --- |
| Ship-to addresses and any other PII | `customer-ordering` and `shipping-and-tracking` only |
| The customer-visible timeline, `PackageDelivered` as a journey state | `shipping-and-tracking` |
| Which delivery station serves a postal prefix, the node registry | `node-topology` (this context keeps a local read model of it) |
| Middle-mile departures and loads | `linehaul-transportation` |
| Quoting, holding and judging promises | `delivery-promise` |
| Choosing the sourcing path | `order-sourcing` |
| Customer returns routing, customer notification | future contexts |
| Route optimisation: the caller sends the stops, the context only validates the plan | not modelled |
| Anything in the warehouse-systems fleet | no edge; the only seam between the organisations is the Vendor API, which this context does not touch |

## Main capabilities

- **Schedule and run waves**: schedule, close induction, dispatch, cancel; capacity published on every change ([Use cases](/docs/ddd/use-cases)).
- **Induct on arrival**: consume `LoadArrived`, place each package, defer it when no wave takes it, sweep waiting packages when a new wave is scheduled.
- **Plan and run routes**: plan within the van capacity, start (one `PackageOutForDelivery` per package), record attempts, complete automatically.
- **Count failures**: three failed attempts publish `PackageReturnInitiated`.
- **Publish facts**: eight CloudEvents types through a transactional outbox ([Domain events](/docs/ddd/domain-events)).
- **Read surfaces for agents**: MCP (three tools) and A2A (two skills) over the same read use cases ([MCP tools](/docs/mcp/tools), [API reference](/docs/api-reference)).

## Where to go next

| I want to... | Page |
| --- | --- |
| understand the layout and processes | [Architecture](/docs/overview/architecture) |
| run it and make the first calls | [Quickstart](/docs/overview/quickstart) |
| set environment variables | [Configuration](/docs/operations/configuration) |
| deploy and operate it | [Runbook](/docs/operations/runbook), [Before the first release](/docs/operations/before-first-release) |
| watch it | [Observability](/docs/operations/observability) |
| fix it | [Troubleshooting](/docs/operations/troubleshooting) |
| test it | [Testing](/docs/development/testing) |
| see who it talks to | [Integration](/docs/ecosystem/integration), [Context map](/docs/ddd/context-map) |
| call it | [API reference](/docs/api-reference), [Event catalogue](/docs/api-reference/events), [MCP tools](/docs/mcp/tools) |
| learn the domain | [Ubiquitous language](/docs/ddd/ubiquitous-language), [DDD artifacts](/docs/ddd/ddd-artifacts), [Use cases](/docs/ddd/use-cases) |
| read the decisions | [ADR index](/docs/adr) |

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company; all names, figures and examples are illustrative or synthetic.
