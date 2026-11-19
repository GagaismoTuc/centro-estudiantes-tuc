-- +goose Up
-- Optional link from a Fund Entry to an Event budget and Heading (issue #81).
-- Entries remain append-only (ce_app keeps only SELECT and INSERT).
-- Composite foreign keys enforce tenant isolation (ADR 0001 rule 10), and ON DELETE RESTRICT
-- prevents deleting a budget or heading that already has linked entries.
ALTER TABLE finance_entries
    ADD COLUMN event_budget_id uuid,
    ADD COLUMN heading_id uuid,
    ADD CONSTRAINT finance_entries_event_link_pair_check
        CHECK ((event_budget_id IS NULL) = (heading_id IS NULL)),
    ADD CONSTRAINT finance_entries_event_budget_fkey
        FOREIGN KEY (tenant_id, event_budget_id) REFERENCES event_budgets (tenant_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT finance_entries_heading_fkey
        FOREIGN KEY (tenant_id, heading_id) REFERENCES contact_headings (tenant_id, id) ON DELETE RESTRICT;

CREATE INDEX finance_entries_event_budget_idx
    ON finance_entries (tenant_id, event_budget_id, heading_id)
    WHERE event_budget_id IS NOT NULL;

-- +goose Down
DROP INDEX finance_entries_event_budget_idx;
ALTER TABLE finance_entries
    DROP CONSTRAINT finance_entries_heading_fkey,
    DROP CONSTRAINT finance_entries_event_budget_fkey,
    DROP CONSTRAINT finance_entries_event_link_pair_check,
    DROP COLUMN heading_id,
    DROP COLUMN event_budget_id;
