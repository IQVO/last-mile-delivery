-- The day-of ETA windows of started routes (ADR 0013): the E3 KPI read side.
--
-- One row per package of a route.StopEtaWindowAssigned event (the event carries
-- the packages of one stop, exploded at projection by one idempotent
-- statement). The CloudEvents time of the event is the assignment instant, the
-- start of the route. An ETA is never re-raised for the same start, so the
-- first row of a (route, package) is the only one.
CREATE TABLE stop_eta_windows (
    route_id        TEXT        NOT NULL CHECK (route_id <> ''),
    tracking_number TEXT        NOT NULL CHECK (tracking_number <> ''),
    stop_no         INTEGER     NOT NULL CHECK (stop_no >= 1),
    eta_start       TIMESTAMPTZ NOT NULL,
    eta_end         TIMESTAMPTZ NOT NULL,
    assigned_at     TIMESTAMPTZ NOT NULL,
    event_id        TEXT        NOT NULL,
    PRIMARY KEY (route_id, tracking_number),
    CHECK (eta_start < eta_end)
);

-- A package's ETA rows across the routes it rode.
CREATE INDEX idx_stop_eta_windows_tracking ON stop_eta_windows (tracking_number);
