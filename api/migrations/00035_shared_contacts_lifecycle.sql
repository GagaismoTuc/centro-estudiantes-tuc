-- +goose Up
-- Tenant-scoped withdrawal records for shared contacts without widening ce_app grants on
-- shared_contacts or shared_contact_links (issue #82, ADR 0005).

ALTER TABLE shared_contacts DROP CONSTRAINT shared_contacts_status_check;
ALTER TABLE shared_contacts ADD CONSTRAINT shared_contacts_status_check
    CHECK (status IN ('pending', 'approved', 'rejected', 'removed', 'withdrawn'));

ALTER TABLE shared_contact_links
    ADD CONSTRAINT shared_contact_links_tenant_shared_key UNIQUE (tenant_id, shared_contact_id);

CREATE TABLE shared_contact_withdrawals (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES organizations (id),
    shared_contact_id uuid NOT NULL REFERENCES shared_contacts (id) ON DELETE CASCADE,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (shared_contact_id),
    FOREIGN KEY (tenant_id, shared_contact_id)
        REFERENCES shared_contact_links (tenant_id, shared_contact_id) ON DELETE CASCADE
);

GRANT SELECT, INSERT ON shared_contact_withdrawals TO ce_app;
ALTER TABLE shared_contact_withdrawals ENABLE ROW LEVEL SECURITY;
ALTER TABLE shared_contact_withdrawals FORCE ROW LEVEL SECURITY;

CREATE POLICY shared_contact_withdrawals_tenant_isolation ON shared_contact_withdrawals
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose StatementBegin
CREATE FUNCTION apply_shared_contact_withdrawal() RETURNS trigger
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public, pg_temp
AS $$
BEGIN
    UPDATE shared_contacts
    SET status = 'withdrawn',
        removed_at = COALESCE(removed_at, NEW.created_at)
    WHERE id = NEW.shared_contact_id
      AND status IN ('pending', 'approved');
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER shared_contact_withdrawals_apply
    AFTER INSERT ON shared_contact_withdrawals
    FOR EACH ROW
    EXECUTE FUNCTION apply_shared_contact_withdrawal();

-- +goose Down
DROP TRIGGER shared_contact_withdrawals_apply ON shared_contact_withdrawals;
DROP FUNCTION apply_shared_contact_withdrawal();
DROP TABLE shared_contact_withdrawals;
ALTER TABLE shared_contact_links DROP CONSTRAINT shared_contact_links_tenant_shared_key;
UPDATE shared_contacts SET status = 'removed' WHERE status = 'withdrawn';
ALTER TABLE shared_contacts DROP CONSTRAINT shared_contacts_status_check;
ALTER TABLE shared_contacts ADD CONSTRAINT shared_contacts_status_check
    CHECK (status IN ('pending', 'approved', 'rejected', 'removed'));
