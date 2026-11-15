-- +goose Up
ALTER TABLE memberships DROP CONSTRAINT memberships_tenant_id_user_id_key;
ALTER TABLE memberships ADD CONSTRAINT memberships_tenant_id_user_id_role_key UNIQUE (tenant_id, user_id, role);

-- +goose Down
ALTER TABLE memberships DROP CONSTRAINT memberships_tenant_id_user_id_role_key;
ALTER TABLE memberships ADD CONSTRAINT memberships_tenant_id_user_id_key UNIQUE (tenant_id, user_id);
