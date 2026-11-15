-- +goose Up
-- Electoral commission: the roster members designated to run one election. One current commission per
-- school; its mandate ends (ended_at) when the result is published or the election is annulled.
CREATE TABLE electoral_commissions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES organizations (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    ended_at   timestamptz NULL,
    -- Target of the composite foreign keys below (FK checks bypass RLS).
    UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX electoral_commissions_one_current ON electoral_commissions (tenant_id) WHERE ended_at IS NULL;

CREATE TABLE electoral_commission_members (
    tenant_id        uuid NOT NULL REFERENCES organizations (id),
    commission_id    uuid NOT NULL,
    roster_member_id uuid NOT NULL,
    PRIMARY KEY (tenant_id, commission_id, roster_member_id),
    FOREIGN KEY (tenant_id, commission_id) REFERENCES electoral_commissions (tenant_id, id),
    FOREIGN KEY (tenant_id, roster_member_id) REFERENCES roster_members (tenant_id, id)
);
CREATE INDEX electoral_commission_members_roster_member_idx ON electoral_commission_members (tenant_id, roster_member_id);

CREATE TABLE elections (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            uuid NOT NULL REFERENCES organizations (id),
    commission_id        uuid NOT NULL,
    name                 text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
    mode                 text NOT NULL CHECK (mode = 'secret'),
    submission_starts_at timestamptz NOT NULL,
    submission_ends_at   timestamptz NOT NULL,
    starts_at            timestamptz NOT NULL,
    ends_at              timestamptz NOT NULL,
    published_at         timestamptz NULL,
    annulled_at          timestamptz NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CHECK (submission_starts_at < submission_ends_at),
    CHECK (submission_ends_at <= starts_at),
    CHECK (starts_at < ends_at),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, commission_id) REFERENCES electoral_commissions (tenant_id, id)
);
-- One election in progress (neither published nor annulled) per school: of two concurrent inserts,
-- exactly one commits and the other fails with unique_violation on this index.
CREATE UNIQUE INDEX elections_one_in_progress ON elections (tenant_id) WHERE published_at IS NULL AND annulled_at IS NULL;
CREATE INDEX elections_commission_idx ON elections (tenant_id, commission_id);

CREATE TABLE election_positions (
    tenant_id      uuid NOT NULL REFERENCES organizations (id),
    election_id    uuid NOT NULL,
    position_order int NOT NULL CHECK (position_order > 0),
    name           text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
    PRIMARY KEY (tenant_id, election_id, position_order),
    FOREIGN KEY (tenant_id, election_id) REFERENCES elections (tenant_id, id)
);
CREATE UNIQUE INDEX election_positions_name_key ON election_positions (tenant_id, election_id, lower(name));

-- No DELETE on commissions or elections: they end (ended_at) or are annulled, never erased.
GRANT SELECT, INSERT, UPDATE ON electoral_commissions TO ce_app;
GRANT SELECT, INSERT, DELETE ON electoral_commission_members TO ce_app;
GRANT SELECT, INSERT, UPDATE ON elections TO ce_app;
GRANT SELECT, INSERT, DELETE ON election_positions TO ce_app;

-- Same fail-closed rule as roster_members (00001): with no tenant set they match zero rows.
ALTER TABLE electoral_commissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE electoral_commissions FORCE ROW LEVEL SECURITY;
CREATE POLICY electoral_commissions_tenant_isolation ON electoral_commissions TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE electoral_commission_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE electoral_commission_members FORCE ROW LEVEL SECURITY;
CREATE POLICY electoral_commission_members_tenant_isolation ON electoral_commission_members TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE elections ENABLE ROW LEVEL SECURITY;
ALTER TABLE elections FORCE ROW LEVEL SECURITY;
CREATE POLICY elections_tenant_isolation ON elections TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE election_positions ENABLE ROW LEVEL SECURITY;
ALTER TABLE election_positions FORCE ROW LEVEL SECURITY;
CREATE POLICY election_positions_tenant_isolation ON election_positions TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP TABLE election_positions;
DROP TABLE elections;
DROP TABLE electoral_commission_members;
DROP TABLE electoral_commissions;
