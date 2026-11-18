-- +goose Up
-- Secret ballot (issue #56): the Electorate snapshot, the Voter registry and the Ballot box.
-- Nothing here stores a time, an account, a session or a link between a Ballot and its voter.

-- Target of the composite foreign key that ties a Ballot's list to its own election.
ALTER TABLE election_lists ADD CONSTRAINT election_lists_tenant_election_id_key UNIQUE (tenant_id, election_id, id);

-- Electorate snapshot marker: taken once, when voting opens; size is the turnout denominator.
CREATE TABLE election_electorates (
    tenant_id   uuid NOT NULL REFERENCES organizations (id),
    election_id uuid NOT NULL,
    size        int NOT NULL CHECK (size >= 0),
    PRIMARY KEY (tenant_id, election_id),
    FOREIGN KEY (tenant_id, election_id) REFERENCES elections (tenant_id, id)
);

CREATE TABLE election_electorate_members (
    tenant_id        uuid NOT NULL REFERENCES organizations (id),
    election_id      uuid NOT NULL,
    roster_member_id uuid NOT NULL,
    PRIMARY KEY (tenant_id, election_id, roster_member_id),
    FOREIGN KEY (tenant_id, election_id) REFERENCES election_electorates (tenant_id, election_id),
    FOREIGN KEY (tenant_id, roster_member_id) REFERENCES roster_members (tenant_id, id)
);

-- Voter registry: who voted, never when nor what. Only members of the snapshot can vote, once.
CREATE TABLE election_voters (
    tenant_id        uuid NOT NULL REFERENCES organizations (id),
    election_id      uuid NOT NULL,
    roster_member_id uuid NOT NULL,
    PRIMARY KEY (tenant_id, election_id, roster_member_id),
    FOREIGN KEY (tenant_id, election_id, roster_member_id)
        REFERENCES election_electorate_members (tenant_id, election_id, roster_member_id)
);

-- Ballots cast while the election is open. ce_app has no privilege on it: a pending row is written
-- in the same transaction as its voter row, so its heap position or xmin would link the two.
-- list_id NULL is a Blank vote.
CREATE TABLE election_ballot_pending (
    tenant_id   uuid NOT NULL REFERENCES organizations (id),
    election_id uuid NOT NULL,
    list_id     uuid NULL,
    FOREIGN KEY (tenant_id, election_id) REFERENCES elections (tenant_id, id),
    FOREIGN KEY (tenant_id, election_id, list_id) REFERENCES election_lists (tenant_id, election_id, id)
);
CREATE INDEX election_ballot_pending_election_idx ON election_ballot_pending (tenant_id, election_id);

-- Ballot box: empty while the election is open; after the close it holds every Ballot of the
-- election, written at once in random order with random ids. list_id NULL is a Blank vote.
CREATE TABLE election_ballots (
    tenant_id   uuid NOT NULL REFERENCES organizations (id),
    election_id uuid NOT NULL,
    id          uuid NOT NULL DEFAULT gen_random_uuid(),
    list_id     uuid NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, election_id) REFERENCES elections (tenant_id, id),
    FOREIGN KEY (tenant_id, election_id, list_id) REFERENCES election_lists (tenant_id, election_id, id)
);
CREATE INDEX election_ballots_election_idx ON election_ballots (tenant_id, election_id);

-- No UPDATE or DELETE: a snapshot and a vote are final. ce_app writes votes only through
-- election_ballot_cast, so a voter row never exists without its Ballot nor a Ballot without its voter.
GRANT SELECT, INSERT ON election_electorates TO ce_app;
GRANT SELECT, INSERT ON election_electorate_members TO ce_app;
-- Column grants only: ce_app cannot read system columns (xmin, ctid) of the registry, which would
-- give the voting order, nor the box's id and system columns.
GRANT SELECT (tenant_id, election_id, roster_member_id) ON election_voters TO ce_app;
GRANT SELECT (tenant_id, election_id, list_id) ON election_ballots TO ce_app;

-- Same fail-closed rule as elections (00012): with no tenant set they match zero rows. The tables the
-- functions write also bind their owner (CURRENT_USER here), since FORCE applies to it.
ALTER TABLE election_electorates ENABLE ROW LEVEL SECURITY;
ALTER TABLE election_electorates FORCE ROW LEVEL SECURITY;
CREATE POLICY election_electorates_tenant_isolation ON election_electorates TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE election_electorate_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE election_electorate_members FORCE ROW LEVEL SECURITY;
CREATE POLICY election_electorate_members_tenant_isolation ON election_electorate_members TO ce_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE election_voters ENABLE ROW LEVEL SECURITY;
ALTER TABLE election_voters FORCE ROW LEVEL SECURITY;
CREATE POLICY election_voters_tenant_isolation ON election_voters TO ce_app, CURRENT_USER
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE election_ballot_pending ENABLE ROW LEVEL SECURITY;
ALTER TABLE election_ballot_pending FORCE ROW LEVEL SECURITY;
CREATE POLICY election_ballot_pending_tenant_isolation ON election_ballot_pending TO ce_app, CURRENT_USER
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE election_ballots ENABLE ROW LEVEL SECURITY;
ALTER TABLE election_ballots FORCE ROW LEVEL SECURITY;
CREATE POLICY election_ballots_tenant_isolation ON election_ballots TO ce_app, CURRENT_USER
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- election_ballots_flush, called after the close, writes the whole box of the election at once: when
-- Ballots are pending, it deletes the election's box and pending rows and inserts all of them again
-- in random order with new ids, so the box's storage order is one uniform shuffle of every Ballot
-- (an early call while open is undone by the final one). It returns how many pending Ballots it
-- moved; with none pending it changes nothing and returns 0.
-- +goose StatementBegin
CREATE FUNCTION election_ballots_flush(p_election uuid) RETURNS int
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    t uuid := nullif(current_setting('app.tenant_id', true), '')::uuid;
    n int;
BEGIN
    IF t IS NULL THEN
        RAISE EXCEPTION 'election_ballots_flush: no tenant set' USING ERRCODE = 'insufficient_privilege';
    END IF;
    SELECT count(*) INTO n FROM public.election_ballot_pending WHERE tenant_id = t AND election_id = p_election;
    IF n = 0 THEN
        RETURN 0;
    END IF;
    -- Data-modifying CTEs all run, referenced or not; the statement returns the pending count.
    WITH boxed AS (
        DELETE FROM public.election_ballots WHERE tenant_id = t AND election_id = p_election RETURNING list_id
    ), pending AS (
        DELETE FROM public.election_ballot_pending WHERE tenant_id = t AND election_id = p_election RETURNING list_id
    ), written AS (
        INSERT INTO public.election_ballots (tenant_id, election_id, list_id)
        SELECT t, p_election, list_id FROM (SELECT list_id FROM boxed UNION ALL SELECT list_id FROM pending) b
        -- gen_random_uuid() draws from the strong random source; random() follows the session's
        -- setseed, which the caller controls, and would let it replay the permutation.
        ORDER BY gen_random_uuid()
    )
    SELECT count(*) INTO n FROM pending;
    RETURN n;
END
$$;
-- +goose StatementEnd

-- election_ballot_cast records that p_member voted (a primary key violation, 23505, when they already
-- did) and stores their Ballot as pending (p_list NULL is a Blank vote), in one call.
-- +goose StatementBegin
CREATE FUNCTION election_ballot_cast(p_election uuid, p_member uuid, p_list uuid) RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    t uuid := nullif(current_setting('app.tenant_id', true), '')::uuid;
BEGIN
    IF t IS NULL THEN
        RAISE EXCEPTION 'election_ballot_cast: no tenant set' USING ERRCODE = 'insufficient_privilege';
    END IF;
    INSERT INTO public.election_voters (tenant_id, election_id, roster_member_id) VALUES (t, p_election, p_member);
    INSERT INTO public.election_ballot_pending (tenant_id, election_id, list_id) VALUES (t, p_election, p_list);
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION election_ballots_flush(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION election_ballot_cast(uuid, uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION election_ballots_flush(uuid) TO ce_app;
GRANT EXECUTE ON FUNCTION election_ballot_cast(uuid, uuid, uuid) TO ce_app;

-- +goose Down
DROP FUNCTION election_ballot_cast(uuid, uuid, uuid);
DROP FUNCTION election_ballots_flush(uuid);
DROP TABLE election_ballots;
DROP TABLE election_ballot_pending;
DROP TABLE election_voters;
DROP TABLE election_electorate_members;
DROP TABLE election_electorates;
ALTER TABLE election_lists DROP CONSTRAINT election_lists_tenant_election_id_key;
