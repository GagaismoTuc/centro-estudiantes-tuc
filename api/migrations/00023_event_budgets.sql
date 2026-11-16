-- +goose Up
CREATE TABLE event_budgets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES organizations (id),
    name text NOT NULL CHECK (btrim(name) <> ''),
    starts_on date NOT NULL,
    ends_on date NOT NULL CHECK (ends_on >= starts_on AND ends_on - starts_on < 31),
    archived_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);

CREATE TABLE event_budget_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES organizations (id),
    budget_id uuid NOT NULL,
    heading_id uuid,
    contact_id uuid,
    kind text NOT NULL CHECK (kind IN ('cost', 'ticket', 'foodtruck', 'sponsor', 'due')),
    description text NOT NULL DEFAULT '',
    quantity bigint CHECK (quantity > 0 OR (kind = 'ticket' AND quantity = 0)),
    unit_amount_cents bigint CHECK (unit_amount_cents > 0),
    day date,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, budget_id) REFERENCES event_budgets (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, heading_id) REFERENCES contact_headings (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id) ON DELETE SET NULL (contact_id)
);

CREATE INDEX event_budget_lines_budget_idx ON event_budget_lines (tenant_id, budget_id);
GRANT SELECT, INSERT, UPDATE, DELETE ON event_budgets, event_budget_lines TO ce_app;
ALTER TABLE event_budgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_budgets FORCE ROW LEVEL SECURITY;
ALTER TABLE event_budget_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_budget_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY event_budgets_tenant_isolation ON event_budgets TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY event_budget_lines_tenant_isolation ON event_budget_lines TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP TABLE event_budget_lines;
DROP TABLE event_budgets;
