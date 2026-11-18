-- +goose Up
-- Exemptions (Exenciones): the Board's decision, with a reason, that a Roster member does not owe one Due
-- (ADR 0004). At most one per Due and Roster member; never edited or removed by the application.
CREATE TABLE finance_due_exemptions (
    tenant_id        uuid NOT NULL REFERENCES organizations (id),
    due_id           uuid NOT NULL,
    roster_member_id uuid NOT NULL,
    reason           text NOT NULL CHECK (char_length(btrim(reason)) > 0 AND char_length(reason) <= 500),
    created_by       uuid NOT NULL REFERENCES users (id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, due_id, roster_member_id),
    -- Only a Roster member in the Due's fixed snapshot can be exempt.
    FOREIGN KEY (tenant_id, due_id, roster_member_id)
        REFERENCES finance_due_members (tenant_id, due_id, roster_member_id),
    FOREIGN KEY (tenant_id, due_id) REFERENCES finance_dues (tenant_id, id),
    FOREIGN KEY (tenant_id, roster_member_id) REFERENCES roster_members (tenant_id, id)
);

GRANT SELECT, INSERT ON finance_due_exemptions TO ce_app;

-- Exemptions and #78's Due payments for one Roster member serialize on that member's snapshot row
-- with SELECT ... FOR UPDATE, which PostgreSQL only allows with an UPDATE privilege on some column.
-- Same grant as #78's 00030 (idempotent): changing roster_member_id adds nothing to the DELETE and
-- INSERT ce_app already has, and the exemptions' foreign key rejects it once the member is exempt.
GRANT UPDATE (roster_member_id) ON finance_due_members TO ce_app;

ALTER TABLE finance_due_exemptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance_due_exemptions FORCE ROW LEVEL SECURITY;
CREATE POLICY finance_due_exemptions_tenant_isolation ON finance_due_exemptions
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
-- Keep the shared lock grant while #78's payments still rely on it.
-- +goose StatementBegin
DO $$
BEGIN
    IF to_regclass('public.finance_due_payments') IS NULL THEN
        REVOKE UPDATE (roster_member_id) ON finance_due_members FROM ce_app;
    END IF;
END
$$;
-- +goose StatementEnd
DROP TABLE finance_due_exemptions;
