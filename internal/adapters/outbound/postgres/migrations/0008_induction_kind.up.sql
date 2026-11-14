-- Return-pickup inductions (ADR 0017, roadmap C8-LM): why a package takes a
-- stop of a wave — DELIVERY (the default; rows stored before this migration
-- keep it) or PICKUP (a dormant return collection; written only when the
-- return flow requests it).
ALTER TABLE wave_inductions
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'DELIVERY'
    CONSTRAINT wave_inductions_kind_check CHECK (kind IN ('DELIVERY', 'PICKUP'));
