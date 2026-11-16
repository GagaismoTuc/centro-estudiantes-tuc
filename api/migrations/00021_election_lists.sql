-- +goose Up
-- Election lists and their candidates per position (issue #55).
-- No DELETE on election_lists: lists are rejected with a reason, never erased.
CREATE TABLE election_lists (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES organizations (id),
    election_id       uuid NOT NULL,
    name              text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 80),
    representative_id uuid NOT NULL,
    status            text NOT NULL CHECK (status IN ('pending', 'approved', 'rejected')),
    is_active         boolean NOT NULL DEFAULT true CHECK (is_active = (status <> 'rejected')),
    rejection_reason  text NULL CHECK (rejection_reason IS NULL OR char_length(btrim(rejection_reason)) BETWEEN 1 AND 500),
    reviewed_at       timestamptz NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'rejected' AND rejection_reason IS NOT NULL) OR (status IN ('pending', 'approved') AND rejection_reason IS NULL)),
    -- Targets for composite foreign keys on election_list_candidates (FK checks bypass RLS).
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, election_id, id, is_active),
    FOREIGN KEY (tenant_id, election_id) REFERENCES elections (tenant_id, id),
    FOREIGN KEY (tenant_id, representative_id) REFERENCES roster_members (tenant_id, id)
);
CREATE UNIQUE INDEX election_lists_name_active_key
    ON election_lists (tenant_id, election_id, lower(btrim(name)))
    WHERE status <> 'rejected';
CREATE INDEX election_lists_election_idx ON election_lists (tenant_id, election_id);

CREATE TABLE election_list_candidates (
    tenant_id      uuid NOT NULL REFERENCES organizations (id),
    election_id    uuid NOT NULL,
    list_id        uuid NOT NULL,
    position_order int NOT NULL,
    candidate_id   uuid NOT NULL,
    -- Kept in sync with election_lists.is_active via ON UPDATE CASCADE, so rejecting a list frees
    -- its candidates for other lists while preserving the rejected list's candidate snapshot.
    is_active      boolean NOT NULL DEFAULT true,
    PRIMARY KEY (tenant_id, list_id, position_order),
    UNIQUE (tenant_id, list_id, candidate_id),
    FOREIGN KEY (tenant_id, election_id, list_id, is_active)
        REFERENCES election_lists (tenant_id, election_id, id, is_active) ON UPDATE CASCADE,
    FOREIGN KEY (tenant_id, election_id, position_order)
        REFERENCES election_positions (tenant_id, election_id, position_order),
    FOREIGN KEY (tenant_id, candidate_id)
        REFERENCES roster_members (tenant_id, id)
);
-- A student can be a candidate on at most one pending or approved list per election.
CREATE UNIQUE INDEX election_list_candidates_active_candidate_key
    ON election_list_candidates (tenant_id, election_id, candidate_id)
    WHERE is_active;

GRANT SELECT, INSERT, UPDATE ON election_lists TO ce_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON election_list_candidates TO ce_app;

-- Same fail-closed rule as elections (00012): with no tenant set they match zero rows.
ALTER TABLE election_lists ENABLE ROW LEVEL SECURITY;
ALTER TABLE election_lists FORCE ROW LEVEL SECURITY;
CREATE POLICY election_lists_tenant_isolation ON election_lists TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE election_list_candidates ENABLE ROW LEVEL SECURITY;
ALTER TABLE election_list_candidates FORCE ROW LEVEL SECURITY;
CREATE POLICY election_list_candidates_tenant_isolation ON election_list_candidates TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP TABLE election_list_candidates;
DROP TABLE election_lists;
