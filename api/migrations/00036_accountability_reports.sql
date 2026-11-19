-- +goose Up
-- A Financial accountability report (Rendición de cuentas) is a frozen copy of the Fund report for a
-- period, published by an Officer for a Term (Gestión). The copy lives in report as JSON: Entries,
-- totals per Category with their names at publication, totals per Due and the balances. It never
-- names a Roster member (ADR 0004). Published reports never change: ce_app keeps only SELECT and
-- INSERT.
-- shortcut: term is free text until the Term model (#58) exists; then link the report to it.
CREATE TABLE finance_accountability_reports (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid NOT NULL REFERENCES organizations (id),
    period_from        date NOT NULL,
    period_to          date NOT NULL,
    term               text NOT NULL CHECK (char_length(btrim(term)) > 0),
    published_by       uuid NOT NULL REFERENCES users (id),
    published_by_name  text NOT NULL,
    published_at       timestamptz NOT NULL DEFAULT now(),
    report             jsonb NOT NULL,
    UNIQUE (tenant_id, id),
    CHECK (period_from <= period_to)
);

CREATE INDEX finance_accountability_reports_published_idx
    ON finance_accountability_reports (tenant_id, published_at DESC);

GRANT SELECT, INSERT ON finance_accountability_reports TO ce_app;

ALTER TABLE finance_accountability_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance_accountability_reports FORCE ROW LEVEL SECURITY;
CREATE POLICY finance_accountability_reports_tenant_isolation ON finance_accountability_reports
    TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP TABLE finance_accountability_reports;
