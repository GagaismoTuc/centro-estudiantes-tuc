-- +goose Up
CREATE TABLE officer_positions (
    tenant_id uuid NOT NULL REFERENCES organizations (id),
    roster_member_id uuid NOT NULL,
    position text NOT NULL CHECK (position IN ('Presidente', 'Vicepresidente', 'Secretario', 'Tesorero')),
    PRIMARY KEY (tenant_id, roster_member_id),
    UNIQUE (tenant_id, position),
    FOREIGN KEY (tenant_id, roster_member_id) REFERENCES roster_members (tenant_id, id)
);
GRANT SELECT, INSERT, DELETE ON officer_positions TO ce_app;
ALTER TABLE officer_positions ENABLE ROW LEVEL SECURITY;
ALTER TABLE officer_positions FORCE ROW LEVEL SECURITY;
CREATE POLICY officer_positions_tenant_isolation ON officer_positions TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP TABLE officer_positions;
