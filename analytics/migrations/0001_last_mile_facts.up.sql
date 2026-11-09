-- last-mile-delivery analytics read model (ADR 0012).
--
-- This is the ANALYTICAL database, separate from the OLTP database. It is
-- written only by cmd/lm-projector and read (read-only) by cmd/lm-reports.
-- Everything here is a projection of this context's own two topics
-- (retailnetwork.last-mile-delivery.events and .analytics), not a source of
-- truth, and can be dropped and rebuilt from them.

-- Idempotency: every applied CloudEvents id is recorded here exactly once, in
-- the SAME transaction as its effect on a fact table. occurred_at is the
-- event's CloudEvents `time` (the basis of /reports/freshness).
CREATE TABLE analytics_processed_events (
    event_id    TEXT        PRIMARY KEY,
    event_type  TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per wave, from wave.DeliveryWaveScheduled.
CREATE TABLE waves (
    wave_id         TEXT    PRIMARY KEY CHECK (wave_id <> ''),
    station_node_id TEXT    NOT NULL CHECK (station_node_id <> ''),
    service_date    DATE    NOT NULL,
    stop_capacity   INTEGER NOT NULL CHECK (stop_capacity >= 0),
    event_id        TEXT    NOT NULL
);

-- The inducted stops of a wave: the highest assignedStops of
-- wave.DeliveryWaveCapacityChanged (one event per NEW stop, so it only grows).
CREATE TABLE wave_inductions (
    wave_id        TEXT    PRIMARY KEY CHECK (wave_id <> ''),
    assigned_stops INTEGER NOT NULL CHECK (assigned_stops > 0),
    event_id       TEXT    NOT NULL
);

-- One row per cancelled wave, from wave.DeliveryWaveCancelled.
CREATE TABLE wave_cancellations (
    wave_id      TEXT        PRIMARY KEY CHECK (wave_id <> ''),
    cancelled_at TIMESTAMPTZ NOT NULL,
    event_id     TEXT        NOT NULL
);

-- The latest declared stop cost of a wave, from wave.WaveCostDeclared (the
-- last declaration wins, ADR 0011; 0 is a declared cost).
CREATE TABLE wave_costs (
    wave_id             TEXT        PRIMARY KEY CHECK (wave_id <> ''),
    cost_per_stop_cents BIGINT      NOT NULL CHECK (cost_per_stop_cents >= 0),
    declared_at         TIMESTAMPTZ NOT NULL,
    event_id            TEXT        NOT NULL
);

-- One row per route, from route.RoutePlanned (the analytics topic).
CREATE TABLE route_plans (
    route_id   TEXT    PRIMARY KEY CHECK (route_id <> ''),
    wave_id    TEXT    NOT NULL CHECK (wave_id <> ''),
    dsp_id     TEXT    NOT NULL,
    stop_count INTEGER NOT NULL CHECK (stop_count > 0),
    event_id   TEXT    NOT NULL
);

-- One row per package of a started route, from route.PackageOutForDelivery.
CREATE TABLE route_packages (
    route_id        TEXT        NOT NULL CHECK (route_id <> ''),
    tracking_number TEXT        NOT NULL CHECK (tracking_number <> ''),
    station_node_id TEXT        NOT NULL CHECK (station_node_id <> ''),
    out_at          TIMESTAMPTZ NOT NULL,
    event_id        TEXT        NOT NULL,
    PRIMARY KEY (route_id, tracking_number)
);

-- One row per route.DeliveryAttempted.
--   attempted_at  the attempt's own instant (payload `at`)
CREATE TABLE delivery_attempts (
    event_id        TEXT        PRIMARY KEY,
    route_id        TEXT        NOT NULL CHECK (route_id <> ''),
    tracking_number TEXT        NOT NULL CHECK (tracking_number <> ''),
    outcome         TEXT        NOT NULL CHECK (outcome IN ('DELIVERED', 'FAILED')),
    attempted_at    TIMESTAMPTZ NOT NULL
);

-- One row per packageattempthistory.PackageReturnInitiated.
--   initiated_at  the CloudEvents time (the payload carries no instant)
CREATE TABLE package_returns (
    event_id        TEXT        PRIMARY KEY,
    tracking_number TEXT        NOT NULL CHECK (tracking_number <> ''),
    initiated_at    TIMESTAMPTZ NOT NULL
);

-- The reports join attempts to their route's packages, group a route's
-- attempts, find a package's attempts and filter on the business instants.
CREATE INDEX idx_delivery_attempts_route ON delivery_attempts (route_id, tracking_number);
CREATE INDEX idx_delivery_attempts_tracking ON delivery_attempts (tracking_number, attempted_at);
CREATE INDEX idx_delivery_attempts_attempted_at ON delivery_attempts (attempted_at);
CREATE INDEX idx_package_returns_initiated_at ON package_returns (initiated_at);
CREATE INDEX idx_waves_service_date ON waves (service_date);
CREATE INDEX idx_analytics_processed_events_occurred_at ON analytics_processed_events (occurred_at);
