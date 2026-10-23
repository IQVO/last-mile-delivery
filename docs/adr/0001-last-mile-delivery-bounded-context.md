# 1. Last-Mile Delivery is a bounded context of the retail network

Status: Accepted (bootstrap). Design source: retail-network-docs `model/model.yaml`, context `last-mile-delivery`, and its ADRs RN-0001 and RN-0007.

## Context

The retail network is modelled as ten bounded contexts, each its own service and database. Runs each delivery station: publishes delivery waves (induct cutoff, delivery window, stop capacity) that the promise relies on, plans routes for delivery service partners, and records every delivery attempt.

## Decision

- This repo owns the aggregates DeliveryWave, Route, DeliveryServicePartner; the invariants and error names are those of the model.
- Business decisions of the context:
- A package joins the earliest wave whose induct cutoff it arrived before and that has stop capacity left.
- A route never exceeds its van's stop capacity.
- A package is delivered at most once; a failed attempt returns it to the station for the next wave.
- Events use the network CloudEvents namespace `com.retailnetwork.logistics.last-mile-delivery.<entity>.<Event>` on `retailnetwork.last-mile-delivery.events`
  (analytics-only events on `.analytics`). Published: DeliveryWaveScheduled, DeliveryWaveCapacityChanged, DeliveryWaveCancelled, RoutePlanned, PackageOutForDelivery, DeliveryAttempted. No `warehouse.*` topic is consumed or produced.
- No live REST lookup into sibling contexts except the calls the model lists under `sync_calls`.
- No authentication on REST, consistent with the fleet. PII exists only in customer-ordering and shipping-and-tracking.

## Consequences

Relationships and the full consumed-event list are in the model and in the AsyncAPI contract added with the first feature PR.
