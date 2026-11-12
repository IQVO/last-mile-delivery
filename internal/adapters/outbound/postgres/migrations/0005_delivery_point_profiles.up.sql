-- DeliveryPointProfile (ADR 0014, roadmap C5): what the attempts made at one
-- drop-off point have shown, keyed by the opaque stopKey. Counters and a short
-- ring of strikes only: no address, name, phone or free text. A package that is
-- its own stop ('pkg:<trackingNumber>') never gets a row. strikes is a jsonb
-- array of at most five items, in ring order; flagged_at is NULL while no flag is
-- open. No retention job in this slice (about 300 bytes per distinct address).
CREATE TABLE delivery_point_profiles (
    stop_key        TEXT PRIMARY KEY CHECK (stop_key <> '' AND stop_key NOT LIKE 'pkg:%'),
    attempts        INTEGER NOT NULL,
    delivered       INTEGER NOT NULL,
    safe_place_used INTEGER NOT NULL,
    failed_not_home INTEGER NOT NULL,
    failed_access   INTEGER NOT NULL,
    failed_other    INTEGER NOT NULL,
    strikes         JSONB NOT NULL DEFAULT '[]',
    flagged_at      TIMESTAMPTZ NULL,
    last_attempt_at TIMESTAMPTZ NOT NULL,
    version         BIGINT NOT NULL,
    CONSTRAINT delivery_point_profiles_counters_check CHECK (
        attempts >= 0 AND delivered >= 0 AND safe_place_used >= 0
        AND failed_not_home >= 0 AND failed_access >= 0 AND failed_other >= 0),
    CONSTRAINT delivery_point_profiles_strikes_check CHECK (
        jsonb_typeof(strikes) = 'array' AND jsonb_array_length(strikes) <= 5),
    CONSTRAINT delivery_point_profiles_version_check CHECK (version >= 1)
);
