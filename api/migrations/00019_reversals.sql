-- +goose Up
-- A Reversal is an Entry with reverses_id set. Entries stay append-only (ce_app keeps only SELECT
-- and INSERT), so the link lives on the new row and the original is never updated.
--
-- "A Reversal never targets a Reversal" is declarative: is_reversal is derived from reverses_id,
-- and the foreign key requires the target row to match (tenant_id, id, is_reversal = false),
-- because reverses_target_is_reversal can only be false. The same key keeps the target in the
-- same school and rejects a row that reverses itself. A trigger would read the target under the
-- inserting transaction's snapshot; the foreign key is checked by PostgreSQL with row locks.
ALTER TABLE finance_entries
    ADD COLUMN reverses_id uuid,
    ADD COLUMN is_reversal boolean GENERATED ALWAYS AS (reverses_id IS NOT NULL) STORED,
    ADD COLUMN reverses_target_is_reversal boolean NOT NULL DEFAULT false
        CONSTRAINT finance_entries_reverses_target_check CHECK (NOT reverses_target_is_reversal),
    ADD CONSTRAINT finance_entries_tenant_id_id_is_reversal_key UNIQUE (tenant_id, id, is_reversal),
    -- One Reversal per Entry: this is the one-way door of issue #71.
    ADD CONSTRAINT finance_entries_reverses_id_key UNIQUE (reverses_id),
    ADD CONSTRAINT finance_entries_reverses_fkey FOREIGN KEY (tenant_id, reverses_id, reverses_target_is_reversal)
        REFERENCES finance_entries (tenant_id, id, is_reversal);

-- A Reversal's description is "Reversión: " (11 characters) plus the original's, up to 500.
ALTER TABLE finance_entries
    DROP CONSTRAINT finance_entries_description_check,
    ADD CONSTRAINT finance_entries_description_check CHECK (
        char_length(btrim(description)) BETWEEN 1 AND CASE WHEN reverses_id IS NULL THEN 500 ELSE 511 END);

-- +goose Down
-- Fails while a Reversal description is longer than 500 characters; the links are dropped.
ALTER TABLE finance_entries
    DROP CONSTRAINT finance_entries_description_check,
    ADD CONSTRAINT finance_entries_description_check CHECK (char_length(btrim(description)) BETWEEN 1 AND 500);
ALTER TABLE finance_entries
    DROP CONSTRAINT finance_entries_reverses_fkey,
    DROP CONSTRAINT finance_entries_reverses_id_key,
    DROP CONSTRAINT finance_entries_tenant_id_id_is_reversal_key,
    DROP COLUMN reverses_target_is_reversal,
    DROP COLUMN is_reversal,
    DROP COLUMN reverses_id;
