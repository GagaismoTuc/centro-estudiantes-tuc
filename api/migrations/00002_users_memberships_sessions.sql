-- +goose Up
-- Platform-level accounts. A user may belong to several tenants, so no RLS here.
-- The app may create and update users but never delete them.
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
GRANT SELECT, INSERT, UPDATE ON users TO ce_app;

-- The bootstrap CLI and the future platform admin create tenants through the app role.
GRANT INSERT ON organizations TO ce_app;

-- Lets memberships reference a roster member of the same tenant only (composite foreign key below).
-- Foreign-key checks bypass RLS, so a plain FK on roster_member_id would accept another tenant's row.
ALTER TABLE roster_members ADD CONSTRAINT roster_members_tenant_id_id_key UNIQUE (tenant_id, id);

-- Role of a user inside one tenant. Tenant-owned.
CREATE TABLE memberships (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES organizations (id),
    user_id          uuid NOT NULL REFERENCES users (id),
    role             text NOT NULL CHECK (role IN ('school_admin', 'officer', 'student')),
    roster_member_id uuid NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, user_id),
    FOREIGN KEY (tenant_id, roster_member_id) REFERENCES roster_members (tenant_id, id)
);
-- A roster entry (and so its voting right) belongs to at most one account.
CREATE UNIQUE INDEX memberships_roster_member_id_key ON memberships (roster_member_id) WHERE roster_member_id IS NOT NULL;
CREATE INDEX memberships_user_id_idx ON memberships (user_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON memberships TO ce_app;

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;

-- Same fail-closed rule as roster_members.
CREATE POLICY memberships_tenant_isolation ON memberships
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- Sign-in happens before a tenant is chosen: a user may read their own memberships in every tenant.
-- SELECT only, and app.user_id is written only by db.WithUser; fails closed when unset.
CREATE POLICY memberships_own_user ON memberships
    FOR SELECT
    TO ce_app
    USING (user_id = nullif(current_setting('app.user_id', true), '')::uuid);

-- Server-side sessions. Only the SHA-256 of the cookie token is stored. Platform-level.
CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id    uuid NOT NULL REFERENCES users (id),
    -- Active tenant chosen at sign-in; NULL only when the user has no membership.
    tenant_id  uuid NULL REFERENCES organizations (id),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > created_at)
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
GRANT SELECT, INSERT, DELETE ON sessions TO ce_app;

-- +goose Down
DROP TABLE sessions;
DROP TABLE memberships;
ALTER TABLE roster_members DROP CONSTRAINT roster_members_tenant_id_id_key;
REVOKE INSERT ON organizations FROM ce_app;
DROP TABLE users;
