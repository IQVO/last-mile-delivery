-- Attempt outcome detail (ADR 0014, roadmap C5): the optional category a driver
-- client reports with an attempt. NULL means "not reported" (a legacy client).
-- failure_reason stays as it is; a detail never replaces it.
ALTER TABLE route_attempts
    ADD COLUMN outcome_detail TEXT NULL,
    ADD CONSTRAINT route_attempts_detail_check CHECK (
        outcome_detail IS NULL
        OR (outcome = 'FAILED' AND outcome_detail IN ('NOT_HOME', 'ACCESS_PROBLEM', 'OTHER'))
        OR (outcome = 'DELIVERED' AND outcome_detail = 'SAFE_PLACE_USED'));
