-- +goose Up
-- A Due payment (Pago) is recorded against one Roster member of the Due's fixed snapshot (ADR 0004)
-- and is written in the same transaction as its income Entry in "Cuotas". Payments are append-only:
-- ce_app keeps only SELECT and INSERT. A Reversal of the payment's Entry discounts the payment, so
-- the amount paid is derived from payments whose Entry has no Reversal; the row is never updated.
CREATE TABLE finance_due_payments (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid NOT NULL REFERENCES organizations (id),
    due_id             uuid NOT NULL,
    roster_member_id   uuid NOT NULL,
    entry_id           uuid NOT NULL,
    -- Always false: lets the foreign key below require that the Entry is not a Reversal.
    entry_is_reversal  boolean NOT NULL DEFAULT false
        CONSTRAINT finance_due_payments_entry_is_reversal_check CHECK (NOT entry_is_reversal),
    amount_cents       bigint NOT NULL CHECK (amount_cents > 0),
    payment_date       date NOT NULL,
    recorded_by        uuid NOT NULL REFERENCES users (id),
    recorded_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    -- One payment per Entry.
    UNIQUE (entry_id),
    -- Composite keys keep the Due, the Roster member and the Entry in the payment's school. The
    -- snapshot row (finance_due_members) references the Due and the Roster member, so a payment can
    -- only be recorded for someone who owes the Due; none of these keys cascade.
    FOREIGN KEY (tenant_id, due_id) REFERENCES finance_dues (tenant_id, id),
    FOREIGN KEY (tenant_id, roster_member_id) REFERENCES roster_members (tenant_id, id),
    FOREIGN KEY (tenant_id, due_id, roster_member_id)
        REFERENCES finance_due_members (tenant_id, due_id, roster_member_id),
    FOREIGN KEY (tenant_id, entry_id, entry_is_reversal)
        REFERENCES finance_entries (tenant_id, id, is_reversal)
);

CREATE INDEX finance_due_payments_member_idx
    ON finance_due_payments (tenant_id, due_id, roster_member_id);

GRANT SELECT, INSERT ON finance_due_payments TO ce_app;

-- Concurrent payments for one Roster member serialize on that member's snapshot row with
-- SELECT ... FOR UPDATE, which PostgreSQL only allows with an UPDATE privilege on some column.
-- Changing roster_member_id adds nothing to the DELETE and INSERT ce_app already has on this
-- table, and the payments' foreign key rejects it once the member has a payment.
GRANT UPDATE (roster_member_id) ON finance_due_members TO ce_app;

ALTER TABLE finance_due_payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance_due_payments FORCE ROW LEVEL SECURITY;
CREATE POLICY finance_due_payments_tenant_isolation ON finance_due_payments
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
REVOKE UPDATE (roster_member_id) ON finance_due_members FROM ce_app;
DROP TABLE finance_due_payments;
