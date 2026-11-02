---
id: class-diagram
title: Class diagram
sidebar_label: Class diagram
sidebar_position: 16
---

# Class diagram

The domain types as built, then the hexagonal ports and the adapters that implement them. Go has no classes; each box is a struct or interface and the members are the exported methods that matter. Aggregates are in `internal/domain/{wave,route,partner,history,shared}`; ports in `internal/application/ports/ports.go`.

## Domain model

```mermaid
classDiagram
    class DeliveryWave {
        +ID id
        +string stationNodeId
        +string serviceDate
        +Time inductCutoffAt
        +Time windowStart
        +Time windowEnd
        +int stopCapacity
        +int assignedStops
        +State state
        +string cancelReason
        +int64 version
        +Schedule(NewWave) DeliveryWave
        +Induct(trackingNumber, stopKey, at) Events
        +CloseInduction()
        +Dispatch()
        +Complete()
        +Cancel(reason) Events
    }
    class Induction {
        +string trackingNumber
        +string stopKey
        +Time at
    }
    class Route {
        +ID id
        +wave.ID waveId
        +partner.ID dspId
        +string stationNodeId
        +State state
        +int64 version
        +Plan(stops, Planning) Route
        +Start() Events
        +RecordAttempt(trackingNumber, outcome, reason, at) Events
        +AllAttempted() bool
        +Complete()
    }
    class Stop {
        +int no
        +string[] trackingNumbers
    }
    class Attempt {
        +string trackingNumber
        +Outcome outcome
        +string failureReason
        +Time at
    }
    class DeliveryServicePartner {
        +ID id
        +string stationNodeId
        +int vanCapacity
        +State state
        +int64 version
        +Contract(NewPartner) DeliveryServicePartner
        +Deactivate()
    }
    class History {
        +string trackingNumber
        +string stationNodeId
        +string assignmentId
        +int failedAttempts
        +Status status
        +int64 version
        +Arrive(...) History
        +Place()
        +Unplace() Events
        +WouldReturn() bool
        +RecordFailure(placed) Events
        +RecordDelivered()
    }
    class Event {
        <<interface>>
        +EventName() string
        +Entity() string
        +SubjectID() string
        +OccurredAt() Time
    }
    DeliveryWave "1" *-- "0..*" Induction
    Route "1" *-- "1..*" Stop
    Route "1" *-- "0..*" Attempt
    Route ..> DeliveryWave : reads at Plan, by id
    Route ..> DeliveryServicePartner : reads at Plan, by id
    History ..> DeliveryWave : placed by use case, by tracking number
    DeliveryWave ..|> Event : raises 2
    Route ..|> Event : raises 3
    History ..|> Event : raises 2
```

Source: `internal/domain/wave/wave.go`, `route/route.go`, `partner/partner.go`, `history/history.go`, `shared/shared.go`, and the three `events.go`. The dotted `..>` arrows are read-by-id relations enforced in the use case, never object references.

## Domain events

```mermaid
classDiagram
    class Event {
        <<interface>>
    }
    class DeliveryWaveScheduled
    class DeliveryWaveCapacityChanged
    class DeliveryWaveCancelled
    class RoutePlanned
    class PackageOutForDelivery
    class DeliveryAttempted
    class PackageInductionDeferred
    class PackageReturnInitiated
    Event <|.. DeliveryWaveScheduled
    Event <|.. DeliveryWaveCapacityChanged
    Event <|.. DeliveryWaveCancelled
    Event <|.. RoutePlanned
    Event <|.. PackageOutForDelivery
    Event <|.. DeliveryAttempted
    Event <|.. PackageInductionDeferred
    Event <|.. PackageReturnInitiated
```

Source: `internal/domain/{wave,route,history}/events.go`. Entities: `wave`, `route`, `packageattempthistory`.

## Application layer and ports

```mermaid
classDiagram
    class UnitOfWork {
        <<interface>>
        +Do(ctx, fn) error
    }
    class WaveRepository {
        <<interface>>
        +Get(id)
        +List(filter)
        +Save(wave)
    }
    class RouteRepository {
        <<interface>>
        +Get(id)
        +PackagesInWave(waveId)
        +CountUncompleted(waveId)
        +Save(route)
    }
    class PartnerRepository {
        <<interface>>
    }
    class HistoryRepository {
        <<interface>>
        +ListWaiting(station, limit)
    }
    class Inbox {
        <<interface>>
        +Claim(eventId, eventType) bool
    }
    class EventOutbox {
        <<interface>>
        +Append(events)
    }
    class EventEncoder {
        <<interface>>
        +Encode(events) Messages
    }
    class ReadModelRepositories {
        <<interface>>
        Station, ServiceArea, PackageStop, InboundPackage
    }
    class Deps {
        +Waves
        +Routes
        +Partners
        +Histories
        +Outbox
        +UoW
        +Clock
        +IDs
        +Metrics
    }
    class ScheduleWave
    class DispatchWave
    class CancelWave
    class PlanRoute
    class StartRoute
    class RecordAttempt
    class InductOnArrival
    class ListWaves
    class GetRoute
    Deps o-- WaveRepository
    Deps o-- RouteRepository
    Deps o-- PartnerRepository
    Deps o-- HistoryRepository
    Deps o-- ReadModelRepositories
    Deps o-- Inbox
    Deps o-- EventOutbox
    Deps o-- UnitOfWork
    EventOutbox ..> EventEncoder
    ScheduleWave ..> Deps
    DispatchWave ..> Deps
    CancelWave ..> Deps
    PlanRoute ..> Deps
    StartRoute ..> Deps
    RecordAttempt ..> Deps
    InductOnArrival ..> Deps
    ListWaves ..> Deps
    GetRoute ..> Deps
```

Source: `internal/application/ports/ports.go`, `internal/application/usecases/*.go` (`Deps`, `writer`, one struct per use case; also `CloseInduction`, `ContractPartner`, `DeactivatePartner`, `GetWaveStops`, `ApplyNodeRegistered`, `ApplyServiceAreaAssigned`, `ApplyLabelIssued`).

## Adapters that exist

| Port | Adapter | Package |
| --- | --- | --- |
| `WaveRepository`, `RouteRepository`, `PartnerRepository`, `HistoryRepository`, read-model repositories | pgx repositories | `internal/adapters/outbound/postgres` |
| `UnitOfWork` | transaction in the context (`pgtx`) | `.../postgres`, `.../postgres/pgtx` |
| `Inbox` | `processed_events` claim | `.../postgres` |
| `EventOutbox` + `EventEncoder` | outbox append with trace headers + CloudEvents encoder | `.../postgres`, `.../kafka` |
| outbox `Store` / `Sink` | `OutboxRepo` (`SKIP LOCKED`) / Kafka relay sink | `.../postgres`, `.../kafka`, relay in `.../outbox` |
| `Clock`, `IDGenerator` | wall clock, uuid ids | `.../system` |
| `AttemptMetrics` | OTel counter | `.../telemetry` |
| inbound | chi REST, three Kafka consumers, MCP server, A2A server | `.../inbound/{http,kafka,mcp,a2a}` |
| test doubles | in-memory repositories (never linked into `cmd/**`) | `.../outbound/memory` |
