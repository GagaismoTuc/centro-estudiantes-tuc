-- +goose Up
ALTER TABLE finance_entries
    ADD CONSTRAINT finance_entries_tenant_id_id_key UNIQUE (tenant_id, id);

CREATE TABLE finance_receipts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES organizations (id),
    entry_id uuid NOT NULL,
    content_type text NOT NULL CHECK (content_type IN ('image/jpeg', 'image/png', 'application/pdf')),
    size_bytes integer NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
    data bytea NOT NULL CHECK (octet_length(data) = size_bytes),
    uploaded_by uuid NOT NULL REFERENCES users (id),
    uploaded_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, entry_id) REFERENCES finance_entries (tenant_id, id)
);

CREATE INDEX finance_receipts_entry_idx ON finance_receipts (tenant_id, entry_id, uploaded_at, id);

GRANT SELECT, INSERT ON finance_receipts TO ce_app;
ALTER TABLE finance_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY finance_receipts_tenant_isolation ON finance_receipts TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP TABLE finance_receipts;
ALTER TABLE finance_entries
    DROP CONSTRAINT finance_entries_tenant_id_id_key;
