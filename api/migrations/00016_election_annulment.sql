-- +goose Up
-- Annulment (issue #59): the public reason the Electoral commission gives when it annuls an election.
-- An election is annulled exactly when it has a reason: annulled_at and annulment_reason are set together.
ALTER TABLE elections
    ADD COLUMN annulment_reason text NULL,
    ADD CONSTRAINT elections_annulment_reason_check
        CHECK (annulment_reason IS NULL OR char_length(btrim(annulment_reason)) BETWEEN 1 AND 500),
    ADD CONSTRAINT elections_annulment_pair_check
        CHECK ((annulled_at IS NULL) = (annulment_reason IS NULL));

-- +goose Down
-- Dropping the column drops both constraints with it.
ALTER TABLE elections DROP COLUMN annulment_reason;
