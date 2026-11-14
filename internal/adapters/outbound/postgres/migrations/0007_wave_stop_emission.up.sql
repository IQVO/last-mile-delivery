-- Wave stop-emission declaration (ADR 0016, roadmap C9 provider): the
-- planner's declared marginal greenhouse emission of ONE delivery stop on a
-- wave, in grams of CO2 equivalent (gCo2e). 0 is a legal declared zero; rows
-- stored before this migration keep 0, which reads as "no declaration".
ALTER TABLE delivery_waves
    ADD COLUMN emission_per_stop_g BIGINT NOT NULL DEFAULT 0
    CONSTRAINT delivery_waves_emission_check CHECK (emission_per_stop_g BETWEEN 0 AND 10000000);
