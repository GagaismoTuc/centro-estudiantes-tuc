-- +goose Up
-- Single-use access codes a school hands to roster members. Tenant-owned.
-- Only HMAC-SHA256(ACCESS_CODE_KEY, normalized code) is stored; code_hash is unique across tenants
-- so the claim flow can find a code before a tenant is known.
CREATE TABLE access_codes (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES organizations (id),
    roster_member_id uuid NOT NULL,
    code_hash        bytea NOT NULL UNIQUE CHECK (octet_length(code_hash) = 32),
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL,
    used_at          timestamptz NULL,
    revoked_at       timestamptz NULL,
    CHECK (expires_at > created_at),
    -- Composite key: FK checks bypass RLS (ADR 0001, tenant isolation rule 10).
    FOREIGN KEY (tenant_id, roster_member_id) REFERENCES roster_members (tenant_id, id)
);
-- At most one code per member that is neither used nor revoked (an expired one still counts until
-- revoked, which issuing a new code does).
CREATE UNIQUE INDEX access_codes_one_active_key ON access_codes (roster_member_id)
    WHERE used_at IS NULL AND revoked_at IS NULL;
CREATE INDEX access_codes_tenant_id_idx ON access_codes (tenant_id);

-- No DELETE: revoked and used codes stay as history.
GRANT SELECT, INSERT, UPDATE ON access_codes TO ce_app;

ALTER TABLE access_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE access_codes FORCE ROW LEVEL SECURITY;

-- Same fail-closed rule as roster_members.
CREATE POLICY access_codes_tenant_isolation ON access_codes
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP TABLE access_codes;
