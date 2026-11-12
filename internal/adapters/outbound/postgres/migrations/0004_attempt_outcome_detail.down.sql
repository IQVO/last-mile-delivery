ALTER TABLE route_attempts DROP CONSTRAINT IF EXISTS route_attempts_detail_check;
ALTER TABLE route_attempts DROP COLUMN IF EXISTS outcome_detail;
