DELETE FROM idempotency_keys;
ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys DROP COLUMN content_type;
ALTER TABLE idempotency_keys ADD COLUMN route TEXT NOT NULL DEFAULT '';
ALTER TABLE idempotency_keys ALTER COLUMN route DROP DEFAULT;
ALTER TABLE idempotency_keys ADD PRIMARY KEY (route, idempotency_key);
