---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
sidebar_position: 19
---

# Sequence diagrams

Adapter, use case, aggregate, repository and outbox for the main flows, with the error branches. Every write goes through `UnitOfWork.Do` (one Postgres transaction) and ends with events appended to `outbox_events` in that transaction. Participants: `HTTP` is the chi adapter with the `Idempotency-Key` middleware, `UC` the use case, `DB` Postgres through the repositories, `Relay` the outbox relay, `Kafka` the broker.

## 1. Schedule a wave

```mermaid
sequenceDiagram
    autonumber
    actor SP as Station planner
    participant HTTP
    participant UC as ScheduleWave
    participant DB as Postgres
    participant Relay
    participant Kafka
    SP->>HTTP: POST /waves + Idempotency-Key
    HTTP->>DB: lock and look up key
    alt key stored with the same body
        DB-->>HTTP: stored 201 response
        HTTP-->>SP: 201 replay with Idempotency-Replayed true
    else new key
        HTTP->>UC: Execute(command)
        UC->>DB: begin, read rm_stations
        alt unknown station
            UC-->>HTTP: ErrUnknownStation
            HTTP-->>SP: 422 unknown-station
        else known station
            UC->>UC: wave.Schedule checks cutoff before window start before window end
            UC->>DB: insert delivery_waves (OPEN)
            UC->>DB: sweep waiting packages into waves (AT_STATION, oldest first)
            UC->>DB: append DeliveryWaveScheduled and CapacityChanged to outbox_events
            UC->>DB: commit
            HTTP->>DB: store response under the key
            HTTP-->>SP: 201 wave
        end
    end
    Relay->>DB: claim unpublished rows (SKIP LOCKED)
    Relay->>Kafka: publish retailnetwork.last-mile-delivery.events
    Relay->>DB: mark published_at
```

Source: `internal/adapters/inbound/http/idempotency.go`, `internal/application/usecases/waves.go` (`ScheduleWave`), `induct.go` (`sweepWaiting`), `internal/adapters/outbound/outbox/relay.go`.

## 2. A load arrives and its packages are inducted

```mermaid
sequenceDiagram
    autonumber
    participant Kafka
    participant C as linehaul consumer
    participant UC as InductOnArrival
    participant DB as Postgres
    Kafka->>C: LoadArrived (CloudEvents)
    C->>C: decode, dispatch on full type
    C->>UC: Execute(command)
    UC->>DB: begin, claim event id in processed_events
    alt already processed
        UC-->>C: nothing to do
    else new event
        UC->>UC: station? nodeType on the event, else rm_stations
        loop every tracking number
            UC->>DB: upsert rm_inbound_packages
            UC->>DB: read rm_package_stops for the stop key
            UC->>DB: list OPEN waves whose cutoff is after arrival, earliest first
            UC->>UC: Induct into the first wave that is not full or closed
            UC->>DB: save wave and PackageAttemptHistory (INDUCTED or AT_STATION)
        end
        UC->>DB: append CapacityChanged and InductionDeferred to outbox_events
        UC->>DB: commit
    end
    C->>Kafka: commit offset after success
```

On a transient error the unit of work rolls back (the event id is un-claimed) and the same message is retried, up to five attempts, then dead-lettered to `<topic>.dlq`. Source: `internal/adapters/inbound/kafka/{consumer,consumers,kafka}.go`, `internal/application/usecases/consumers.go`, `induct.go`.

## 3. Plan and start a route

```mermaid
sequenceDiagram
    autonumber
    actor SP as Station planner
    actor Driver as DSP driver app
    participant HTTP
    participant UC as PlanRoute / StartRoute
    participant DB as Postgres
    SP->>HTTP: POST /routes (waveId, dspId, stops)
    HTTP->>UC: PlanRoute
    UC->>DB: read wave, partner, packages already on routes of the wave
    UC->>UC: route.Plan checks wave INDUCTION_CLOSED, partner active, same station, stops within van capacity, packages inducted and unrouted, one stopKey per stop
    alt a rule fails
        UC-->>HTTP: domain error
        HTTP-->>SP: 409 or 422 with the problem slug
    else valid
        UC->>DB: insert route PLANNED, append RoutePlanned (analytics topic)
        HTTP-->>SP: 201 route
    end
    SP->>HTTP: POST /waves/{id}/dispatches
    Note over HTTP,DB: wave DISPATCHED, packages held by no route are re-inducted
    Driver->>HTTP: POST /routes/{id}/starts
    HTTP->>UC: StartRoute
    UC->>DB: read route and its wave
    alt wave not DISPATCHED or route not PLANNED
        HTTP-->>Driver: 409 illegal-transition
    else ok
        UC->>DB: route OUT_FOR_DELIVERY, append PackageOutForDelivery per package
        HTTP-->>Driver: 200 route
    end
```

Source: `internal/application/usecases/routes.go`, `internal/domain/route/route.go` (`Plan`, `checkPlanning`, `Start`).

## 4. Record an attempt (delivered, failed, third failure)

```mermaid
sequenceDiagram
    autonumber
    actor Driver as DSP driver app
    participant HTTP
    participant UC as RecordAttempt
    participant DB as Postgres
    Driver->>HTTP: POST /routes/{id}/attempts (trackingNumber, outcome, reason, at)
    HTTP->>UC: Execute
    UC->>DB: begin, load route
    UC->>UC: route.RecordAttempt checks OUT_FOR_DELIVERY, package on route, no previous attempt
    alt rule fails
        UC-->>HTTP: route-not-started, package-not-on-route, attempt-already-recorded or already-delivered
        HTTP-->>Driver: 404 or 409 problem, counter outcome rejected
    else outcome DELIVERED
        UC->>DB: history DELIVERED (unique delivered-once index)
    else outcome FAILED, fewer than 2 earlier failures
        UC->>DB: place the package on the next eligible wave (RetryNextWave)
        UC->>DB: history failed + 1
    else outcome FAILED, third failure
        UC->>DB: history RETURN_INITIATED, append PackageReturnInitiated
    end
    UC->>DB: save route, append DeliveryAttempted
    opt every package of the route has an attempt
        UC->>DB: route COMPLETED
        opt no uncompleted route left in the wave
            UC->>DB: wave COMPLETED
        end
    end
    UC->>DB: commit
    HTTP-->>Driver: 201 attempt
```

Source: `internal/application/usecases/routes.go` (`RecordAttempt`, `updateHistory`, `completeRouteIfDone`), `internal/domain/history/history.go` (`RecordFailure`, `WouldReturn`).

## 5. Cancel a wave

```mermaid
sequenceDiagram
    autonumber
    actor SP as Station planner
    participant HTTP
    participant UC as CancelWave
    participant DB as Postgres
    SP->>HTTP: POST /waves/{id}/cancellations (reason)
    HTTP->>UC: Execute
    UC->>DB: load wave
    alt wave is not OPEN
        HTTP-->>SP: 409 illegal-transition
    else OPEN
        UC->>DB: wave CANCELLED, append DeliveryWaveCancelled
        loop every package the wave held
            UC->>DB: place on the next eligible OPEN wave
            alt no wave takes it
                UC->>DB: history AT_STATION, append PackageInductionDeferred
            end
        end
        HTTP-->>SP: 200 wave
    end
```

Source: `internal/application/usecases/waves.go` (`CancelWave`), `induct.go` (`reinduct`).

## 6. Read through MCP

```mermaid
sequenceDiagram
    autonumber
    participant Agent as AI agent
    participant MCP as cmd/mcp
    participant UC as ListWaves
    participant DB as Postgres
    Agent->>MCP: initialize, then tools/call list_waves
    MCP->>UC: Execute(stationNodeId, serviceDate)
    UC->>DB: select waves
    alt invalid input
        MCP-->>Agent: isError validation-failed
    else ok
        MCP-->>Agent: structuredContent items
    end
```

The A2A `list_waves` and `get_route` skills call the same read use cases. Source: `internal/adapters/inbound/mcp/tools.go`, `internal/adapters/inbound/a2a/skills.go`.
