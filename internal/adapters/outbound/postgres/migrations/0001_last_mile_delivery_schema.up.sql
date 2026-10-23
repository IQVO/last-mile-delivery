-- last-mile-delivery schema (ADR 0002, ADR 0003). One table per aggregate root,
-- child tables for the collections it owns, rm_ tables for the event-fed read
-- models and four infrastructure tables. No foreign key crosses an aggregate or
-- a context: ids of other aggregates are carried by value.

-- DeliveryWave: stop capacity is counted in stops (distinct stop keys of the
-- inductions), see ADR 0002 A4.
CREATE TABLE delivery_waves (
    wave_id          TEXT        PRIMARY KEY,
    station_node_id  TEXT        NOT NULL,
    service_date     DATE        NOT NULL,
    induct_cutoff_at TIMESTAMPTZ NOT NULL,
    window_start     TIMESTAMPTZ NOT NULL,
    window_end       TIMESTAMPTZ NOT NULL,
    stop_capacity    INTEGER     NOT NULL,
    assigned_stops   INTEGER     NOT NULL DEFAULT 0,
    state            TEXT        NOT NULL,
    cancel_reason    TEXT,
    version          BIGINT      NOT NULL,
    CONSTRAINT delivery_waves_state_check CHECK (state IN ('OPEN', 'INDUCTION_CLOSED', 'DISPATCHED', 'COMPLETED', 'CANCELLED')),
    CONSTRAINT delivery_waves_times_check CHECK (induct_cutoff_at < window_start AND window_start < window_end),
    CONSTRAINT delivery_waves_capacity_check CHECK (stop_capacity >= 0 AND assigned_stops >= 0 AND assigned_stops <= stop_capacity),
    CONSTRAINT delivery_waves_cancel_reason_check CHECK ((state = 'CANCELLED') = (cancel_reason IS NOT NULL)),
    CONSTRAINT delivery_waves_version_check CHECK (version >= 1)
);
CREATE INDEX idx_delivery_waves_station_date ON delivery_waves (station_node_id, service_date);
CREATE INDEX idx_delivery_waves_open_cutoff ON delivery_waves (station_node_id, induct_cutoff_at) WHERE state = 'OPEN';

CREATE TABLE wave_inductions (
    wave_id         TEXT        NOT NULL REFERENCES delivery_waves (wave_id),
    tracking_number TEXT        NOT NULL,
    stop_key        TEXT        NOT NULL,
    inducted_at     TIMESTAMPTZ NOT NULL,
    position        INTEGER     NOT NULL,
    PRIMARY KEY (wave_id, tracking_number),
    CONSTRAINT wave_inductions_position_key UNIQUE (wave_id, position)
);

-- Route: ordered stops, the packages of each stop and the attempts (append only).
CREATE TABLE routes (
    route_id        TEXT   PRIMARY KEY,
    wave_id         TEXT   NOT NULL,
    dsp_id          TEXT   NOT NULL,
    station_node_id TEXT   NOT NULL,
    state           TEXT   NOT NULL,
    version         BIGINT NOT NULL,
    CONSTRAINT routes_state_check CHECK (state IN ('PLANNED', 'OUT_FOR_DELIVERY', 'COMPLETED')),
    CONSTRAINT routes_version_check CHECK (version >= 1)
);
CREATE INDEX idx_routes_wave ON routes (wave_id);

CREATE TABLE route_stops (
    route_id TEXT    NOT NULL REFERENCES routes (route_id),
    stop_no  INTEGER NOT NULL,
    PRIMARY KEY (route_id, stop_no),
    CONSTRAINT route_stops_stop_no_check CHECK (stop_no >= 1)
);

CREATE TABLE route_stop_packages (
    route_id        TEXT    NOT NULL,
    stop_no         INTEGER NOT NULL,
    tracking_number TEXT    NOT NULL,
    position        INTEGER NOT NULL,
    PRIMARY KEY (route_id, tracking_number),
    FOREIGN KEY (route_id, stop_no) REFERENCES route_stops (route_id, stop_no),
    CONSTRAINT route_stop_packages_position_key UNIQUE (route_id, stop_no, position)
);

CREATE TABLE route_attempts (
    route_id        TEXT        NOT NULL REFERENCES routes (route_id),
    tracking_number TEXT        NOT NULL,
    outcome         TEXT        NOT NULL,
    failure_reason  TEXT,
    attempted_at    TIMESTAMPTZ NOT NULL,
    position        INTEGER     NOT NULL,
    PRIMARY KEY (route_id, tracking_number),
    CONSTRAINT route_attempts_outcome_check CHECK (outcome IN ('DELIVERED', 'FAILED')),
    CONSTRAINT route_attempts_reason_check CHECK ((outcome = 'FAILED') = (failure_reason IS NOT NULL))
);
-- A package is delivered at most once, across routes (ErrAlreadyDelivered).
CREATE UNIQUE INDEX idx_route_attempts_delivered_once ON route_attempts (tracking_number) WHERE outcome = 'DELIVERED';

CREATE TABLE delivery_service_partners (
    dsp_id          TEXT    PRIMARY KEY,
    station_node_id TEXT    NOT NULL,
    van_capacity    INTEGER NOT NULL,
    state           TEXT    NOT NULL,
    version         BIGINT  NOT NULL,
    CONSTRAINT delivery_service_partners_van_capacity_check CHECK (van_capacity > 0),
    CONSTRAINT delivery_service_partners_state_check CHECK (state IN ('ACTIVE', 'INACTIVE')),
    CONSTRAINT delivery_service_partners_version_check CHECK (version >= 1)
);

-- PackageAttemptHistory (Gap Register G16): one row per tracking number.
CREATE TABLE package_histories (
    tracking_number TEXT        PRIMARY KEY,
    station_node_id TEXT        NOT NULL,
    assignment_id   TEXT,
    failed_attempts INTEGER     NOT NULL,
    status          TEXT        NOT NULL,
    arrived_at      TIMESTAMPTZ NOT NULL,
    version         BIGINT      NOT NULL,
    CONSTRAINT package_histories_status_check CHECK (status IN ('AT_STATION', 'INDUCTED', 'DELIVERED', 'RETURN_INITIATED')),
    CONSTRAINT package_histories_failed_check CHECK (failed_attempts BETWEEN 0 AND 3),
    CONSTRAINT package_histories_version_check CHECK (version >= 1)
);
CREATE INDEX idx_package_histories_waiting ON package_histories (station_node_id, arrived_at, tracking_number) WHERE status = 'AT_STATION';

-- Read models, fed by events and rebuilt from them; no aggregate points at them.
CREATE TABLE rm_stations (
    node_id   TEXT PRIMARY KEY,
    node_type TEXT NOT NULL,
    region_id TEXT,
    timezone  TEXT
);

CREATE TABLE rm_station_service_areas (
    postal_prefix      TEXT   PRIMARY KEY,
    station_node_id    TEXT   NOT NULL,
    region_id          TEXT,
    assignment_version BIGINT NOT NULL
);

CREATE TABLE rm_package_stops (
    tracking_number           TEXT PRIMARY KEY,
    stop_key                  TEXT NOT NULL,
    destination_postal_prefix TEXT,
    assignment_id             TEXT
);

CREATE TABLE rm_inbound_packages (
    load_id         TEXT        NOT NULL,
    tracking_number TEXT        NOT NULL,
    departure_id    TEXT,
    station_node_id TEXT        NOT NULL,
    arrived_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (load_id, tracking_number)
);

-- outbox_events: the transactional outbox. One row per already-encoded Kafka
-- message, inserted in the SAME transaction as the aggregates; the relay drains
-- the unpublished rows. event_id is the CloudEvents id, minted once and stored,
-- so a relay retry republishes the same id.
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- processed_events: the inbox. The CloudEvents id is claimed with INSERT ... ON
-- CONFLICT DO NOTHING in the SAME transaction as the effect, so a rollback
-- un-claims it.
CREATE TABLE processed_events (
    event_id     TEXT        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- idempotency_keys: the Idempotency-Key middleware (decision D3): keyed by
-- (route, key), replayed for 24 hours. Only completed 2xx responses are kept.
CREATE TABLE idempotency_keys (
    route            TEXT        NOT NULL,
    idempotency_key  TEXT        NOT NULL,
    request_hash     TEXT        NOT NULL,
    response_status  INTEGER     NOT NULL,
    response_body    BYTEA       NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (route, idempotency_key)
);
CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
