-- +goose Up
-- Platform-level shared contact directory and tenant-scoped sharing links (issue #77, ADR 0005).

CREATE TABLE shared_contacts (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'removed')),
    heading_name     text NOT NULL CHECK (btrim(heading_name) <> ''),
    name             text NOT NULL CHECK (btrim(name) <> ''),
    phone            text NOT NULL DEFAULT '',
    email            text NOT NULL DEFAULT '',
    zone             text NOT NULL DEFAULT '',
    reference_prices jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(reference_prices) = 'array'),
    created_at       timestamptz NOT NULL DEFAULT now(),
    reviewed_at      timestamptz NULL,
    removed_at       timestamptz NULL
);

CREATE INDEX shared_contacts_status_name_idx ON shared_contacts (status, lower(name), id);
CREATE INDEX shared_contacts_status_heading_idx ON shared_contacts (status, lower(heading_name));

CREATE TABLE shared_contact_links (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    seq               bigint GENERATED ALWAYS AS IDENTITY,
    tenant_id         uuid NOT NULL REFERENCES organizations (id),
    contact_id        uuid NOT NULL,
    shared_contact_id uuid NOT NULL REFERENCES shared_contacts (id) ON DELETE CASCADE,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (shared_contact_id),
    FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX shared_contact_links_tenant_contact_idx
    ON shared_contact_links (tenant_id, contact_id, seq DESC);

GRANT SELECT, INSERT ON shared_contacts TO ce_app;
ALTER TABLE shared_contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE shared_contacts FORCE ROW LEVEL SECURITY;

-- +goose StatementBegin
DO $$
BEGIN
    -- The table owner (MIGRATE_DATABASE_URL) runs admin moderation commands and needs full access.
    EXECUTE format('CREATE POLICY shared_contacts_owner ON shared_contacts TO %I USING (true) WITH CHECK (true)', current_user);
END
$$;
-- +goose StatementEnd

-- App role reads approved entries across all schools, plus the active tenant's own shared links
-- (so a school sees "pending" or "rejected" on its own Contact); never UPDATE or DELETE.
CREATE POLICY shared_contacts_app_select ON shared_contacts
    FOR SELECT
    TO ce_app
    USING (
        status = 'approved'
        OR EXISTS (
            SELECT 1 FROM shared_contact_links l
            WHERE l.shared_contact_id = shared_contacts.id
              AND l.tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid
        )
    );

CREATE POLICY shared_contacts_app_insert ON shared_contacts
    FOR INSERT
    TO ce_app
    WITH CHECK (status = 'pending');

GRANT SELECT, INSERT ON shared_contact_links TO ce_app;
ALTER TABLE shared_contact_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE shared_contact_links FORCE ROW LEVEL SECURITY;

CREATE POLICY shared_contact_links_tenant_isolation ON shared_contact_links
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP POLICY shared_contacts_app_select ON shared_contacts;
DROP TABLE shared_contact_links;
DROP TABLE shared_contacts;
