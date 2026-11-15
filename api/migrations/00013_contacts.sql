-- +goose Up
-- Contact directory headings, contacts, and dated reference prices (issue #70).

CREATE TABLE contact_headings (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES organizations (id),
    name        text NOT NULL CHECK (btrim(name) <> ''),
    archived_at timestamptz NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX contact_headings_tenant_name_lower_idx
    ON contact_headings (tenant_id, lower(name));

-- Seed default headings for every existing school before forcing RLS on the table owner.
INSERT INTO contact_headings (tenant_id, name)
SELECT o.id, v.name
FROM organizations o
CROSS JOIN (
    VALUES
        ('artistas'),
        ('foodtrucks'),
        ('mesas y sillas'),
        ('gazebos'),
        ('cristalería'),
        ('insumos plásticos'),
        ('sonido e iluminación'),
        ('seguridad'),
        ('impresiones'),
        ('otros')
) AS v(name)
WHERE o.kind = 'school'
ON CONFLICT (tenant_id, lower(name)) DO NOTHING;

GRANT SELECT, INSERT, UPDATE, DELETE ON contact_headings TO ce_app;
ALTER TABLE contact_headings ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_headings FORCE ROW LEVEL SECURITY;

CREATE POLICY contact_headings_tenant_isolation ON contact_headings
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE contacts (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES organizations (id),
    heading_id uuid NOT NULL,
    name       text NOT NULL CHECK (btrim(name) <> ''),
    phone      text NOT NULL DEFAULT '',
    email      text NOT NULL DEFAULT '',
    zone       text NOT NULL DEFAULT '',
    notes      text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, heading_id) REFERENCES contact_headings (tenant_id, id) ON DELETE RESTRICT
);

CREATE INDEX contacts_tenant_heading_idx ON contacts (tenant_id, heading_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON contacts TO ce_app;
ALTER TABLE contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE contacts FORCE ROW LEVEL SECURITY;

CREATE POLICY contacts_tenant_isolation ON contacts
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE contact_reference_prices (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES organizations (id),
    contact_id   uuid NOT NULL,
    description  text NOT NULL CHECK (btrim(description) <> ''),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    quoted_on    date NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX contact_reference_prices_tenant_contact_idx
    ON contact_reference_prices (tenant_id, contact_id, quoted_on DESC, created_at DESC);

GRANT SELECT, INSERT, DELETE ON contact_reference_prices TO ce_app;
ALTER TABLE contact_reference_prices ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_reference_prices FORCE ROW LEVEL SECURITY;

CREATE POLICY contact_reference_prices_tenant_isolation ON contact_reference_prices
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP TABLE contact_reference_prices;
DROP TABLE contacts;
DROP TABLE contact_headings;
