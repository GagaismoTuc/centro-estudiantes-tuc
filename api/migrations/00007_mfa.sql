-- +goose Up
-- TOTP second factor. Platform-level like users and sessions (a user is not tied to one tenant), so
-- no RLS. The secret is stored only encrypted: AES-256-GCM under TOTP_ENC_KEY with a random nonce and
-- the user id as additional data (nonce 12 + secret 20 + tag 16 bytes).
CREATE TABLE user_totp (
    user_id        uuid PRIMARY KEY REFERENCES users (id),
    secret_enc     bytea NOT NULL CHECK (octet_length(secret_enc) = 48),
    -- NULL while enrollment is pending; a pending secret may be replaced.
    confirmed_at   timestamptz NULL,
    -- Last accepted time step: codes of this step or earlier are replays.
    last_used_step bigint NULL
);
-- Delete is the operator's reset path (admin reset-mfa). Updates only replace a pending secret,
-- confirm it and record the last step; user_id is never rewritten.
GRANT SELECT, INSERT, DELETE ON user_totp TO ce_app;
GRANT UPDATE (secret_enc, confirmed_at, last_used_step) ON user_totp TO ce_app;

-- Single-use recovery codes: only SHA-256 of the 10 Crockford base32 characters is stored.
CREATE TABLE recovery_codes (
    code_hash bytea PRIMARY KEY CHECK (octet_length(code_hash) = 32),
    user_id   uuid NOT NULL REFERENCES users (id),
    used_at   timestamptz NULL
);
CREATE INDEX recovery_codes_user_id_idx ON recovery_codes (user_id);
GRANT SELECT, INSERT, DELETE ON recovery_codes TO ce_app;
GRANT UPDATE (used_at) ON recovery_codes TO ce_app;

-- When the session proved the second factor. Never updated in place: verifying inserts a new session
-- (new token) and deletes the old one, so ce_app gets no UPDATE on this column.
ALTER TABLE sessions ADD COLUMN mfa_at timestamptz NULL;

-- +goose Down
ALTER TABLE sessions DROP COLUMN mfa_at;
DROP TABLE recovery_codes;
DROP TABLE user_totp;
