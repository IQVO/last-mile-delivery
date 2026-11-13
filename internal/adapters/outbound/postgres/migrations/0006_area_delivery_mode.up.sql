-- Carrier-area exclusion (ADR 0015, roadmap C4): the delivery mode a service
-- area's assignment declared. NETWORK_ROUTE (the default, and the behaviour of
-- every area until node-topology declares otherwise) means the station's own
-- vans carry the final leg and the packages induct into waves as today;
-- CARRIER_HANDOFF means a contracted carrier carries the final leg from a
-- handoff node and last-mile delivery refuses induction for the prefix. The
-- value rides ServiceAreaAssigned additively; an event without the field (an
-- old producer or an old replay) decodes to the default before it reaches the
-- table, and a lower assignment version never overwrites a stored mode.
ALTER TABLE rm_station_service_areas
    ADD COLUMN delivery_mode TEXT NOT NULL DEFAULT 'NETWORK_ROUTE'
    CHECK (delivery_mode IN ('NETWORK_ROUTE', 'CARRIER_HANDOFF'));
