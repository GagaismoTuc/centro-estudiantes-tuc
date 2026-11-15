-- +goose Up
CREATE TABLE finance_categories (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES organizations (id),
    direction text NOT NULL CHECK (direction IN ('income', 'expense')),
    name text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    reserved boolean NOT NULL DEFAULT false,
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, direction, name)
);

CREATE TABLE finance_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES organizations (id),
    category_id uuid NOT NULL,
    direction text NOT NULL CHECK (direction IN ('income', 'expense')),
    entry_date date NOT NULL,
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    description text NOT NULL CHECK (char_length(btrim(description)) BETWEEN 1 AND 500),
    recorded_by uuid NOT NULL REFERENCES users (id),
    recorded_by_name text NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, category_id) REFERENCES finance_categories (tenant_id, id)
);

-- This function is invoked in the same transaction as school bootstrap and is safe to retry.
-- +goose StatementBegin
CREATE FUNCTION seed_finance_categories(school_id uuid) RETURNS void LANGUAGE sql AS $$
    INSERT INTO finance_categories (tenant_id, direction, name, reserved)
    SELECT school_id, v.direction, v.name, v.reserved FROM (VALUES
        ('income', 'Cuotas', true),
        ('income', 'Eventos', false),
        ('income', 'Ventas', false),
        ('income', 'Donaciones y sponsors', false),
        ('income', 'Otros ingresos', false),
        ('expense', 'Eventos', false),
        ('expense', 'Materiales e impresiones', false),
        ('expense', 'Alquileres', false),
        ('expense', 'Viajes', false),
        ('expense', 'Otros gastos', false)
    ) AS v(direction, name, reserved)
    ON CONFLICT (tenant_id, direction, name) DO NOTHING;
$$;
-- +goose StatementEnd

SELECT seed_finance_categories(id) FROM organizations WHERE kind = 'school';

GRANT SELECT, INSERT ON finance_categories TO ce_app;
GRANT SELECT, INSERT ON finance_entries TO ce_app;
ALTER TABLE finance_categories ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance_categories FORCE ROW LEVEL SECURITY;
CREATE POLICY finance_categories_tenant_isolation ON finance_categories TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE finance_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY finance_entries_tenant_isolation ON finance_entries TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP FUNCTION seed_finance_categories(uuid);
DROP TABLE finance_entries;
DROP TABLE finance_categories;
