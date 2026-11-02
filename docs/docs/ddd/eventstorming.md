---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
sidebar_position: 17
---

# EventStorming

Design-level EventStorming of the last mile, following the [EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet). Sticky-note colours: **orange** domain event, **blue** command, **yellow** aggregate, **lilac** policy, **green** read model, **pink** external system or hotspot. Events are named as the code publishes them; read models are the `rm_*` tables.

Source: `internal/application/usecases/*.go`, `internal/domain/*/events.go`, `internal/adapters/inbound/kafka/consumers.go`.

## Process level: from arrival to the doorstep

```mermaid
flowchart LR
    classDef event fill:#f5a623,stroke:#a86b00,color:#000
    classDef command fill:#4a90d9,stroke:#1f4f87,color:#fff
    classDef agg fill:#f8e71c,stroke:#8a7e00,color:#000
    classDef policy fill:#b39ddb,stroke:#5e4b9a,color:#000
    classDef rm fill:#7ed321,stroke:#3f7a0b,color:#000
    classDef ext fill:#f48fb1,stroke:#a8325e,color:#000

    LA(["LoadArrived<br/>(linehaul)"]):::ext
    LI(["LabelIssued<br/>(shipping)"]):::ext
    NR(["NodeRegistered<br/>(node-topology)"]):::ext
    RMS[("rm_stations")]:::rm
    RMP[("rm_package_stops")]:::rm
    NR --> RMS
    LI --> RMP

    SW["ScheduleWave"]:::command
    W1{{"DeliveryWave"}}:::agg
    E1["DeliveryWaveScheduled"]:::event
    RMS -.-> SW
    SW --> W1 --> E1

    P1["InductOnArrival"]:::policy
    LA --> P1
    RMP -.-> P1
    P1 --> W1
    W1 --> E2["DeliveryWaveCapacityChanged"]:::event
    P1 --> H1{{"PackageAttemptHistory"}}:::agg
    H1 --> E3["PackageInductionDeferred"]:::event
    E1 --> P2["InductDeferredOnSchedule"]:::policy
    P2 --> W1

    CI["CloseInduction"]:::command --> W1
    PR["PlanRoute"]:::command --> R1{{"Route"}}:::agg
    R1 --> E4["RoutePlanned<br/>(analytics)"]:::event
    DW["DispatchWave"]:::command --> W1
    SR["StartRoute"]:::command --> R1
    R1 --> E5["PackageOutForDelivery"]:::event
    RA["RecordAttempt"]:::command --> R1
    R1 --> E6["DeliveryAttempted"]:::event
    E6 --> P3["RetryNextWave"]:::policy
    P3 --> W1
    P3 --> H1
    H1 --> E7["PackageReturnInitiated"]:::event
    R1 --> P4["CompleteWaveOnLastRoute"]:::policy --> W1
    CW["CancelWave"]:::command --> W1
    W1 --> E8["DeliveryWaveCancelled"]:::event
    CW --> P5["re-induct the wave's packages"]:::policy --> W1
```

## Hotspots

| Hotspot | What the code does today |
| --- | --- |
| A wave is full or closed when a package arrives | the package is tried against the next eligible wave, else it waits `AT_STATION` and `PackageInductionDeferred` is published |
| A wave is cancelled with packages in it | they are re-inducted onto the next eligible wave in the same transaction (G17), else deferred |
| The wave is dispatched while packages are on no route | they are re-inducted (A11) |
| Two packages for one address | one stop: they share a `stopKey`, take one capacity unit and must travel on one stop of a route |
| Label data not yet arrived at induction | the package is its own stop (`pkg:<trackingNumber>`) |
| A partner is deactivated with planned routes | the planned routes are not re-checked (G34: only new routes need an active partner) |
| Nobody consumes the cancel, deferred and return events | published for visibility and a future returns or notification context |
| A driver reports a delivered package twice | `already-delivered`, enforced in the aggregate and by a partial unique index |
| An attempt arrives on a route that was never started | `route-not-started` (409) |

## Policies

| Policy | Trigger | Reaction |
| --- | --- | --- |
| `InductOnArrival` | `LoadArrived` | induct every package into the earliest eligible wave, record the arrival, defer the unplaced |
| `InductDeferredOnSchedule` | a wave is scheduled | induct the packages waiting at the station, oldest first, at most 1000 |
| `RetryNextWave` | a `FAILED` attempt | place the package on the next eligible wave unless this is the third failure |
| `CompleteWaveOnLastRoute` | dispatch, or a route completes | complete the `DISPATCHED` wave when no route is left uncompleted |
| re-induction | cancel, dispatch | move the packages the wave cannot run |

## Big picture

```mermaid
flowchart LR
    A["Station set up<br/>NodeRegistered"] --> B["Waves scheduled"]
    B --> C["Loads arrive,<br/>packages inducted"]
    C --> D["Induction closed,<br/>routes planned"]
    D --> E["Wave dispatched,<br/>routes started"]
    E --> F["Attempts recorded"]
    F -->|"delivered"| G["Wave completed"]
    F -->|"failed"| C
    F -->|"third failure"| H["Return to sender"]
```
