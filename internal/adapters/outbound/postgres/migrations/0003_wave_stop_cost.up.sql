-- Wave stop-cost declaration (ADR 0011, roadmap F7 provider B): the planner's
-- declared marginal cost of ONE delivery stop on a wave, integer cents,
-- single-currency study. 0 is a legal declared zero; rows stored before this
-- migration keep 0, which reads as "no declaration".
ALTER TABLE delivery_waves
    ADD COLUMN cost_per_stop_cents BIGINT NOT NULL DEFAULT 0
    CONSTRAINT delivery_waves_cost_check CHECK (cost_per_stop_cents BETWEEN 0 AND 10000000);
