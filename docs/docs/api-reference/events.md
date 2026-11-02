---
id: events
title: Event catalogue
sidebar_label: Event catalogue
sidebar_position: 20
---

# Event catalogue

The events of `last-mile-delivery` as pinned by [`apis/asyncapi.yaml`](https://github.com/IQVO/last-mile-delivery/blob/develop/apis/asyncapi.yaml) (AsyncAPI 3.0.0, version 1.0.0) and verified against the encoder by `TestEventCatalogueMatchesContract`. Field-level payloads, the envelope and consumer behaviour are on [Domain events](/docs/ddd/domain-events). All messages are CloudEvents 1.0 in structured mode (`content-type: application/cloudevents+json; charset=UTF-8`), source `/retailnetwork/last-mile-delivery`, subject and Kafka key = aggregate id, `dataschema` `urn:retailnetwork:last-mile-delivery:<stream>:<EventName>:v1`.

Source: `apis/asyncapi.yaml` (channels `lastMileEvents`, `lastMileAnalytics`, `linehaulEvents`, `nodeTopologyEvents`, `shippingEvents`), `internal/adapters/outbound/kafka/encoder.go`.

## Channels

| AsyncAPI channel | Topic | Direction | Operation |
| --- | --- | --- | --- |
| `lastMileEvents` | `retailnetwork.last-mile-delivery.events` | publish | `publishLastMileEvents` |
| `lastMileAnalytics` | `retailnetwork.last-mile-delivery.analytics` | publish | `publishLastMileAnalytics` |
| `linehaulEvents` | `retailnetwork.linehaul-transportation.events` | consume | `receiveLoadArrived` |
| `nodeTopologyEvents` | `retailnetwork.node-topology.events` | consume | `receiveNodeTopologyEvents` |
| `shippingEvents` | `retailnetwork.shipping-and-tracking.events` | consume | `receiveLabelIssued` |

## Published

| Event | Full `type` | Topic | Key |
| --- | --- | --- | --- |
| `DeliveryWaveScheduled` | `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled` | `...last-mile-delivery.events` | `waveId` |
| `DeliveryWaveCapacityChanged` | `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCapacityChanged` | `...last-mile-delivery.events` | `waveId` |
| `DeliveryWaveCancelled` | `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCancelled` | `...last-mile-delivery.events` | `waveId` |
| `PackageOutForDelivery` | `com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery` | `...last-mile-delivery.events` | `routeId` |
| `DeliveryAttempted` | `com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted` | `...last-mile-delivery.events` | `routeId` |
| `PackageInductionDeferred` | `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageInductionDeferred` | `...last-mile-delivery.events` | tracking number |
| `PackageReturnInitiated` | `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated` | `...last-mile-delivery.events` | tracking number |
| `RoutePlanned` | `com.retailnetwork.logistics.last-mile-delivery.route.RoutePlanned` | `...last-mile-delivery.analytics` **only** | `routeId` |

Seven events travel on the events topic and `RoutePlanned` only on the analytics topic. Producer guarantees: written in the aggregate's transaction (transactional outbox), published at least once with a stable CloudEvents `id`, ordered per key, acks=all.

## Consumed

| Producer | Full `type` | Topic |
| --- | --- | --- |
| `linehaul-transportation` | `com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived` | `retailnetwork.linehaul-transportation.events` |
| `node-topology` | `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | `retailnetwork.node-topology.events` |
| `node-topology` | `com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned` | `retailnetwork.node-topology.events` |
| `shipping-and-tracking` | `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued` | `retailnetwork.shipping-and-tracking.events` |

## Rules for changing the catalogue

A new event is one commit: domain struct, `apis/asyncapi.yaml`, the `type:` line of [ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue), the encoder payload and a golden test; the catalogue test fails otherwise. Envelopes are built only by `internal/adapters/kafka/cloudevents` (`TestCloudEventsOnly`).
