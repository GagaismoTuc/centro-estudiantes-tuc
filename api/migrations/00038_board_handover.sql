-- +goose Up
-- Board handover (issue #58): when an election's result is published with a winner, the winning
-- list's candidates become the Board with the positions of that election, which need not be the
-- four default ones. A position follows the same rule as election_positions.name (00012); manual
-- assignment by the School admin still accepts only the four default names (checked in the server).
ALTER TABLE officer_positions DROP CONSTRAINT officer_positions_position_check;
ALTER TABLE officer_positions ADD CONSTRAINT officer_positions_position_check
    CHECK (char_length(btrim(position)) BETWEEN 1 AND 120);

-- The winning list of a published election; NULL while unpublished, annulled, or published without
-- a winner (tie, no votes, no lists). The composite key ties the list to this very election and
-- tenant (FK checks bypass RLS; ADR 0001 rule 10). ce_app already has UPDATE on elections (00012).
ALTER TABLE elections ADD COLUMN winner_list_id uuid NULL;
ALTER TABLE elections ADD CONSTRAINT elections_winner_list_fkey
    FOREIGN KEY (tenant_id, id, winner_list_id) REFERENCES election_lists (tenant_id, election_id, id);

-- +goose Down
-- Restoring the four-name rule fails (23514) while a Board holds a position outside the four default
-- names; such positions must be removed by hand before migrating down.
ALTER TABLE elections DROP CONSTRAINT elections_winner_list_fkey;
ALTER TABLE elections DROP COLUMN winner_list_id;
ALTER TABLE officer_positions DROP CONSTRAINT officer_positions_position_check;
ALTER TABLE officer_positions ADD CONSTRAINT officer_positions_position_check
    CHECK (position IN ('Presidente', 'Vicepresidente', 'Secretario', 'Tesorero'));
