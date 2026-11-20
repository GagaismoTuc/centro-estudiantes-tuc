-- +goose Up
ALTER TABLE memberships DROP CONSTRAINT memberships_role_check;
ALTER TABLE memberships ADD CONSTRAINT memberships_role_check
    CHECK (role IN ('school_admin', 'officer', 'student', 'veedor'));

ALTER TABLE elections ADD COLUMN annulled_by_veedor boolean NOT NULL DEFAULT false;


-- Invitations are scoped to a school. A token holder sees only its own row before choosing a tenant.
CREATE TABLE school_invitations (
    tenant_id uuid NOT NULL REFERENCES organizations (id),
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    email text NOT NULL,
    role text NOT NULL CHECK (role IN ('school_admin', 'veedor')),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    used_at timestamptz NULL
);
CREATE UNIQUE INDEX school_invitations_one_unused ON school_invitations (tenant_id, lower(email)) WHERE used_at IS NULL;
GRANT SELECT, INSERT ON school_invitations TO ce_app;
GRANT UPDATE (used_at) ON school_invitations TO ce_app;
ALTER TABLE school_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY school_invitations_tenant ON school_invitations TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY school_invitations_holder_select ON school_invitations FOR SELECT TO ce_app
    USING (encode(token_hash, 'hex') = nullif(current_setting('app.invitation_hash', true), ''));
CREATE POLICY school_invitations_holder_update ON school_invitations FOR UPDATE TO ce_app
    USING (encode(token_hash, 'hex') = nullif(current_setting('app.invitation_hash', true), ''))
    WITH CHECK (encode(token_hash, 'hex') = nullif(current_setting('app.invitation_hash', true), ''));

-- +goose Down
DROP TABLE school_invitations;
ALTER TABLE elections DROP COLUMN annulled_by_veedor;
ALTER TABLE memberships DROP CONSTRAINT memberships_role_check;
ALTER TABLE memberships ADD CONSTRAINT memberships_role_check
    CHECK (role IN ('school_admin', 'officer', 'student'));
