-- +goose Up
-- Application role: owns nothing and has no BYPASSRLS, so Row-Level Security always applies to it.
-- Real login roles are created per deployment with `IN ROLE ce_app`.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'ce_app') THEN
        CREATE ROLE ce_app NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
END
$$;
-- +goose StatementEnd

-- Platform-level tenants (schools and municipalities). Not tenant-owned, so no RLS.
CREATE TABLE organizations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       text NOT NULL CHECK (kind IN ('school', 'municipality')),
    parent_id  uuid NULL REFERENCES organizations (id),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- School roster (padrón). Tenant-owned.
CREATE TABLE roster_members (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES organizations (id),
    full_name       text NOT NULL,
    document_number text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, document_number)
);
CREATE INDEX roster_members_tenant_id_idx ON roster_members (tenant_id);

GRANT SELECT ON organizations TO ce_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON roster_members TO ce_app;

ALTER TABLE roster_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE roster_members FORCE ROW LEVEL SECURITY;

-- Fail closed: an unset setting yields NULL and an empty one becomes NULL via nullif,
-- so `tenant_id = NULL` matches zero rows instead of raising a cast error.
CREATE POLICY roster_members_tenant_isolation ON roster_members
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
-- Dropping the tables also drops their policy and grants.
-- The ce_app role is cluster-wide (shared by every database on the server), so it is left in place.
DROP TABLE roster_members;
DROP TABLE organizations;
