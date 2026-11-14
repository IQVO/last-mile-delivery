-- Carrier-area exclusion (ADR 0015): the mode column goes, the assignments
-- stay; every area reads as NETWORK_ROUTE again, which is the behaviour the
-- mode default encoded.
ALTER TABLE rm_station_service_areas DROP COLUMN delivery_mode;
