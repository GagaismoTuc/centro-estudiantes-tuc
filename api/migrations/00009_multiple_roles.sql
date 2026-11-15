-- +goose Up
-- The migration owner must see every membership, even though tenant RLS is forced.
-- This is transaction-scoped: any failure restores FORCE before the lock is released.
ALTER TABLE memberships NO FORCE ROW LEVEL SECURITY;
-- +goose StatementBegin
DO $$
DECLARE legacy_count bigint;
BEGIN
    SELECT count(*) INTO legacy_count FROM memberships WHERE role = 'officer';
    IF legacy_count > 0 THEN
        RAISE EXCEPTION '% legacy officer memberships without positions; migration aborted', legacy_count;
    END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;
ALTER TABLE memberships DROP CONSTRAINT memberships_tenant_id_user_id_key;
ALTER TABLE memberships ADD CONSTRAINT memberships_tenant_id_user_id_role_key UNIQUE (tenant_id, user_id, role);

-- +goose Down
ALTER TABLE memberships DROP CONSTRAINT memberships_tenant_id_user_id_role_key;
ALTER TABLE memberships ADD CONSTRAINT memberships_tenant_id_user_id_key UNIQUE (tenant_id, user_id);
