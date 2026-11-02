---
id: entity-relationship
title: Entity-relationship diagram
sidebar_label: Entity-relationship
sidebar_position: 18
---

# Entity-relationship diagram

The Postgres schema created by the single migration `internal/adapters/outbound/postgres/migrations/0001_last_mile_delivery_schema.up.sql`: **15 tables**, embedded in the binary and applied at boot by golang-migrate. One table per aggregate root, child tables for the collections it owns, four `rm_` read-model tables and three infrastructure tables. No foreign key crosses an aggregate or a context: ids of other aggregates are carried by value, so the diagrams below show the real foreign keys only inside an aggregate.

## Aggregates

```mermaid
erDiagram
    delivery_waves ||--o{ wave_inductions : "wave_id"
    routes ||--o{ route_stops : "route_id"
    route_stops ||--o{ route_stop_packages : "route_id, stop_no"
    routes ||--o{ route_attempts : "route_id"

    delivery_waves {
        text wave_id PK
        text station_node_id
        date service_date
        timestamptz induct_cutoff_at
        timestamptz window_start
        timestamptz window_end
        int stop_capacity
        int assigned_stops
        text state "OPEN, INDUCTION_CLOSED, DISPATCHED, COMPLETED, CANCELLED"
        text cancel_reason
        bigint version
    }
    wave_inductions {
        text wave_id PK
        text tracking_number PK
        text stop_key
        timestamptz inducted_at
        int position "unique per wave"
    }
    routes {
        text route_id PK
        text wave_id "by value, no FK"
        text dsp_id "by value, no FK"
        text station_node_id
        text state "PLANNED, OUT_FOR_DELIVERY, COMPLETED"
        bigint version
    }
    route_stops {
        text route_id PK
        int stop_no PK
    }
    route_stop_packages {
        text route_id PK
        text tracking_number PK
        int stop_no
        int position
    }
    route_attempts {
        text route_id PK
        text tracking_number PK
        text outcome "DELIVERED, FAILED"
        text failure_reason
        timestamptz attempted_at
        int position
    }
```

Source: `0001_last_mile_delivery_schema.up.sql` (`delivery_waves`, `wave_inductions`, `routes`, `route_stops`, `route_stop_packages`, `route_attempts`).

Key constraints: `delivery_waves_times_check` (`induct_cutoff_at < window_start < window_end`), `delivery_waves_capacity_check` (`assigned_stops <= stop_capacity`), `delivery_waves_cancel_reason_check` (`CANCELLED` exactly when a reason exists); indexes `idx_delivery_waves_station_date` and the partial `idx_delivery_waves_open_cutoff` on `OPEN` waves (the induction scan); `idx_routes_wave`; `route_attempts_reason_check` (a `failure_reason` exactly when `FAILED`); the partial unique index `idx_route_attempts_delivered_once` on `tracking_number WHERE outcome = 'DELIVERED'` (a package is delivered at most once, across routes).

## Partners and package histories

```mermaid
erDiagram
    delivery_service_partners {
        text dsp_id PK
        text station_node_id
        int van_capacity "greater than 0"
        text state "ACTIVE, INACTIVE"
        bigint version
    }
    package_histories {
        text tracking_number PK
        text station_node_id
        text assignment_id
        int failed_attempts "0 to 3"
        text status "AT_STATION, INDUCTED, DELIVERED, RETURN_INITIATED"
        timestamptz arrived_at
        bigint version
    }
```

Source: same migration. `idx_package_histories_waiting` is a partial index on `(station_node_id, arrived_at, tracking_number) WHERE status = 'AT_STATION'`: the sweep that a newly scheduled wave runs.

## Read models (event-fed)

```mermaid
erDiagram
    rm_stations {
        text node_id PK
        text node_type
        text region_id
        text timezone
    }
    rm_station_service_areas {
        text postal_prefix PK
        text station_node_id
        text region_id
        bigint assignment_version
    }
    rm_package_stops {
        text tracking_number PK
        text stop_key
        text destination_postal_prefix
        text assignment_id
    }
    rm_inbound_packages {
        text load_id PK
        text tracking_number PK
        text departure_id
        text station_node_id
        timestamptz arrived_at
    }
```

Source: same migration. Fed by `NodeRegistered` (`rm_stations`), `ServiceAreaAssigned` (`rm_station_service_areas`), `LabelIssued` (`rm_package_stops`) and `LoadArrived` (`rm_inbound_packages`). No aggregate points at them.

## Infrastructure

```mermaid
erDiagram
    outbox_events {
        bigserial id PK
        text event_id "unique with topic"
        text topic
        text event_type
        text subject
        bytea key
        text dataschema
        bytea value
        jsonb headers
        timestamptz created_at
        timestamptz published_at "null until published"
        int attempts
        text last_error
    }
    processed_events {
        text event_id PK
        text event_type
        timestamptz processed_at
    }
    idempotency_keys {
        text route PK
        text idempotency_key PK
        text request_hash
        int response_status
        bytea response_body
        timestamptz created_at
    }
```

Source: same migration.

- `outbox_events` is the transactional outbox. `event_id` is the CloudEvents id minted at encode time, so a relay retry republishes the same id; `(event_id, topic)` is unique; the partial index `idx_outbox_events_unpublished` on `id WHERE published_at IS NULL` serves the relay's claim; `attempts` and `last_error` carry the failure count the attempt cap reads.
- `processed_events` is the inbox: the consumer claims the CloudEvents id with `INSERT ... ON CONFLICT DO NOTHING` in the same transaction as the effect, so a rollback un-claims it.
- `idempotency_keys` holds the `Idempotency-Key` middleware's stored 2xx and 4xx answers (status, content type, body), keyed by the key alone (global; the request hash decides replay or 409); rows older than 24 hours are ignored by the lookup (there is no purge job).

Down migration: `0001_last_mile_delivery_schema.down.sql` in the same directory.
