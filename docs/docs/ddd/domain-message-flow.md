---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
sidebar_position: 15
---

# Domain message flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling). Four scenarios. Participants are contexts and actors. Every arrow is a message prefixed `cmd:` (command), `qry:` (query) or `evt:` (event). Events travel on Kafka through the producer's outbox; the diagrams draw them from producer to consumer and leave Kafka out. Event names are written `<entity>.<EventName>`; the full types are on [Domain events](/docs/ddd/domain-events). Scenarios 2 and 3 are the happy path from arrival to the doorstep, split to stay readable. Scenario 4 is the failure path.

Source: `internal/adapters/inbound/http/server.go`, `internal/adapters/inbound/kafka/consumers.go`, `internal/adapters/outbound/kafka/encoder.go`, `internal/application/usecases/*.go`.

## 1. A wave is scheduled and becomes promisable

```mermaid
sequenceDiagram
    autonumber
    actor SP as Station planner
    participant NT as node-topology
    participant LM as last-mile-delivery
    participant DP as delivery-promise
    participant OS as order-sourcing
    NT->>LM: evt: node.NodeRegistered (DELIVERY_STATION)
    SP->>LM: cmd: ScheduleWave POST /waves
    LM-->>SP: reply 201, wave OPEN
    LM->>DP: evt: wave.DeliveryWaveScheduled
    LM->>OS: evt: wave.DeliveryWaveScheduled
    Note over DP,OS: the wave is now in their event-fed views
    SP->>LM: qry: GET /waves with stationNodeId and serviceDate
    LM-->>SP: reply waves
```

Omitted: the `unknown-station` and `invalid-wave-times` rejections (422) and how `delivery-promise` quotes on the wave.

## 2. A load arrives and its packages are inducted

```mermaid
sequenceDiagram
    autonumber
    participant LH as linehaul-transportation
    participant ST as shipping-and-tracking
    participant LM as last-mile-delivery
    participant DP as delivery-promise
    participant OS as order-sourcing
    ST->>LM: evt: label.LabelIssued (stopKey, postal prefix)
    LH->>LM: evt: load.LoadArrived (trackingNumbers)
    Note over LM: InductOnArrival, earliest eligible wave, stops counted by stopKey
    LM->>DP: evt: wave.DeliveryWaveCapacityChanged
    LM->>OS: evt: wave.DeliveryWaveCapacityChanged
    Note over LM: no wave takes the package, it waits AT_STATION
    LM-->>LM: evt: packageattempthistory.PackageInductionDeferred
```

A later `ScheduleWave` sweeps the waiting packages into the new wave.

## 3. Plan, dispatch, deliver

```mermaid
sequenceDiagram
    autonumber
    actor SP as Station planner
    actor DR as DSP driver app
    participant LM as last-mile-delivery
    participant ST as shipping-and-tracking
    SP->>LM: cmd: CloseInduction POST /waves/id:close-induction
    SP->>LM: cmd: PlanRoute POST /routes
    LM-->>SP: reply 201, route PLANNED
    SP->>LM: cmd: DispatchWave POST /waves/id:dispatch
    DR->>LM: cmd: StartRoute POST /routes/id:start
    LM->>ST: evt: route.PackageOutForDelivery per package
    DR->>LM: cmd: RecordAttempt POST /routes/id/attempts DELIVERED
    LM->>ST: evt: route.DeliveryAttempted
    Note over LM: last package attempted, route COMPLETED, last route done, wave COMPLETED
```

`RoutePlanned` goes to the analytics topic only and has no context consumer.

## 4. A delivery fails

```mermaid
sequenceDiagram
    autonumber
    actor DR as DSP driver app
    participant LM as last-mile-delivery
    participant ST as shipping-and-tracking
    participant DP as delivery-promise
    DR->>LM: cmd: RecordAttempt FAILED with a reason
    LM->>ST: evt: route.DeliveryAttempted (FAILED)
    Note over LM: RetryNextWave, next eligible wave
    LM->>DP: evt: wave.DeliveryWaveCapacityChanged
    DR->>LM: cmd: RecordAttempt FAILED (the third)
    LM->>ST: evt: route.DeliveryAttempted (FAILED)
    LM-->>LM: evt: packageattempthistory.PackageReturnInitiated
```

If no wave takes a package after a failure it waits `AT_STATION` and `PackageInductionDeferred` is published instead of a capacity change. A fifth scenario, cancelling a wave, is on [Sequence diagrams](/docs/ddd/sequence-diagrams): every package it held is re-inducted the same way.
