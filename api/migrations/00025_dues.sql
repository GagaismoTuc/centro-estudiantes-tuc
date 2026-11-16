-- +goose Up
-- Dues (Cuotas) and their fixed snapshot of Roster members at creation time (ADR 0004).
CREATE TABLE finance_dues (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES organizations (id),
    name         text NOT NULL CHECK (char_length(btrim(name)) > 0),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    due_date     date NOT NULL,
    created_by   uuid NOT NULL REFERENCES users (id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);

CREATE INDEX finance_dues_tenant_due_date_idx
    ON finance_dues (tenant_id, due_date DESC, created_at DESC, id DESC);

-- Composite foreign keys enforce tenant isolation; no ON DELETE CASCADE on roster_members
-- so future roster removals keep payment history intact (A12 / R7).
CREATE TABLE finance_due_members (
    tenant_id        uuid NOT NULL REFERENCES organizations (id),
    due_id           uuid NOT NULL,
    roster_member_id uuid NOT NULL,
    PRIMARY KEY (tenant_id, due_id, roster_member_id),
    FOREIGN KEY (tenant_id, due_id) REFERENCES finance_dues (tenant_id, id),
    FOREIGN KEY (tenant_id, roster_member_id) REFERENCES roster_members (tenant_id, id)
);

CREATE INDEX finance_due_members_roster_idx
    ON finance_due_members (tenant_id, roster_member_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON finance_dues TO ce_app;
GRANT SELECT, INSERT, DELETE ON finance_due_members TO ce_app;

ALTER TABLE finance_dues ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance_dues FORCE ROW LEVEL SECURITY;
CREATE POLICY finance_dues_tenant_isolation ON finance_dues
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE finance_due_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance_due_members FORCE ROW LEVEL SECURITY;
CREATE POLICY finance_due_members_tenant_isolation ON finance_due_members
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP TABLE finance_due_members;
DROP TABLE finance_dues;
