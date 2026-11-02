-- REST guideline: the Idempotency-Key is global (looked up by key alone, the
-- request fingerprint decides replay or 409), and 4xx answers are stored with
-- their Content-Type. Rows of the old per-route design are only replay hints
-- for 24 hours; they are dropped.
DELETE FROM idempotency_keys;
ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys DROP COLUMN route;
ALTER TABLE idempotency_keys ADD COLUMN content_type TEXT NOT NULL DEFAULT '';
ALTER TABLE idempotency_keys ADD PRIMARY KEY (idempotency_key);
