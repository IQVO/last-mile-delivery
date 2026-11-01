---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
sidebar_position: 10
---

# Ubiquitous language

The terms of the last mile as the code uses them. Each term carries the identifier that implements it, so a conversation with the station planner and a read of the code use the same words. Neighbour terms are marked with their owner.

Source: `internal/domain/*`, `internal/application/views/views.go`, `apis/openapi.yaml`, `apis/asyncapi.yaml`, [ADR 0002](/docs/adr/0002-aggregates-and-invariants).

| Term | Meaning | Code identifier |
| --- | --- | --- |
| Delivery station | A network-operated node that receives loads and dispatches vans; owned by `node-topology` as node type `DELIVERY_STATION` | `views.DeliveryStation`, table `rm_stations` |
| Delivery wave | A dispatch slot of one station on one service date with an induct cutoff, a delivery window and a stop capacity | `wave.DeliveryWave` |
| Service date | The station's local calendar date a wave runs on | `ServiceDate`, `YYYY-MM-DD` |
| Induct cutoff | The last instant a package can join a wave | `InductCutoffAt` |
| Delivery window | The period in which the wave's packages are delivered | `WindowStart`, `WindowEnd` |
| Stop | One delivery address; several packages for one address are one stop | `route.Stop`, grouped by `stopKey` |
| Stop key | Opaque keyed hash of the normalised address (never the address); `pkg:<trackingNumber>` when no label data arrived | `stopKey`, table `rm_package_stops` |
| Stop capacity | How many stops a wave can take (counted in stops, not packages) | `StopCapacity`, `AssignedStops`, `RemainingStops` |
| Induction | Placing a package in a wave | `DeliveryWave.Induct`, `wave_inductions` |
| Induct on arrival | Policy: when a load arrives at a station, induct every package into the earliest eligible wave | `usecases.InductOnArrival` |
| Eligible wave | An `OPEN` wave of the station whose cutoff is after the package's arrival and that has room or already holds its stop | `writer.place` |
| Deferred package | A package no wave takes yet; it waits `AT_STATION` and `PackageInductionDeferred` is published | `history.StatusAtStation`, `PackageInductionDeferred` |
| Close induction | Stop accepting packages into a wave | `CloseInduction` |
| Dispatch | Release the wave to its routes; packages no route holds are re-inducted | `DispatchWave` |
| Re-induct | Move an inducted package to the next eligible wave (after a cancel, a dispatch leftover or a failed attempt) | `writer.reinduct`, `RetryNextWave` |
| Delivery service partner (DSP) | An independent delivery business contracted to a station, with vans | `partner.DeliveryServicePartner` |
| Van capacity | The most stops one of the partner's routes may have | `VanCapacity` |
| Route | One partner's van run within a wave: ordered stops and attempts | `route.Route` |
| Plan a route | Assign inducted packages, grouped into numbered stops, to a partner | `PlanRoute`, `route.Plan` |
| Out for delivery | A started route; each of its packages is announced | `StateOutForDelivery`, `PackageOutForDelivery` |
| Delivery attempt | One try at one package on one route, `DELIVERED` or `FAILED` with a reason | `route.Attempt`, `DeliveryAttempted` |
| Failure reason | Free text supplied exactly when an attempt failed | `FailureReason` |
| Package attempt history | Per tracking number: status and failed-attempt count (G16) | `history.History`, `package_histories` |
| Return to sender | After three failed attempts the package is returned | `MaxFailedAttempts = 3`, `PackageReturnInitiated` |
| Tracking number | The package's identity, issued upstream | `trackingNumber` |
| Load | A linehaul shipment arriving at a station with its tracking numbers (owner: `linehaul-transportation`) | `LoadArrived` |
| Label | The shipping label of a package carrying the stop key and postal prefix (owner: `shipping-and-tracking`) | `LabelIssued` |
| Service area | The postal prefixes a station serves (owner: `node-topology`) | `ServiceAreaAssigned`, `rm_station_service_areas` |
| Postal prefix | The leading part of the postal code; the only address fragment this context keeps | `destinationPostalPrefix` |
| Read model | A local, event-fed copy of a neighbour's facts | tables `rm_*` |
| Outbox / inbox | The transactional outbox of published events and the `processed_events` table of consumed ones | `outbox_events`, `processed_events` |
| Idempotency key | Client-chosen key that makes a state-changing call safe to repeat for 24 hours | `Idempotency-Key`, `idempotency_keys` |
| Station planner | The person who schedules waves and plans routes | actor |
| DSP driver app | The partner's app that starts routes and reports attempts | actor |

## Distinctions that matter

- **Stop versus package.** Capacity is counted in stops. A second package for an address already in the wave takes no capacity and raises no capacity event.
- **Cancel versus dispatch leftover.** Both re-induct packages, but cancel moves all of them and only an `OPEN` wave can be cancelled; dispatch moves only those no route holds.
- **Failed attempt versus return.** A failed attempt sends the package to the next wave; only the third failure returns it.
- **Wave `COMPLETED` versus route `COMPLETED`.** A route completes when every package has an attempt; the wave completes with its last route.
