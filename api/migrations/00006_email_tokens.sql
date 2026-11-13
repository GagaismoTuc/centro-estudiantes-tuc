-- +goose Up
-- Set when the user proves access to the inbox (verification link or emailed sign-in link).
ALTER TABLE users ADD COLUMN email_verified_at timestamptz NULL;

-- Least privilege on users: the app only verifies emails and changes passwords, never rewrites the
-- email or the id.
REVOKE UPDATE ON users FROM ce_app;
GRANT UPDATE (email_verified_at, password_hash) ON users TO ce_app;

-- Single-use emailed tokens. Platform-level like sessions (a user is not tied to one tenant), so no
-- RLS. Only the SHA-256 of 32 random bytes is stored; the token travels in the link's URL fragment.
CREATE TABLE email_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id    uuid NOT NULL REFERENCES users (id),
    purpose    text NOT NULL CHECK (purpose IN ('verify_email', 'sign_in', 'reset_password')),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz NULL,
    CHECK (expires_at > created_at)
);
-- Issuing a token marks the previous unused one of the same purpose as used: at most one live token.
CREATE UNIQUE INDEX email_tokens_one_unused_key ON email_tokens (user_id, purpose) WHERE used_at IS NULL;
CREATE INDEX email_tokens_user_id_idx ON email_tokens (user_id);

-- No DELETE and no UPDATE of anything but used_at: tokens stay as history.
GRANT SELECT, INSERT ON email_tokens TO ce_app;
GRANT UPDATE (used_at) ON email_tokens TO ce_app;

-- +goose Down
DROP TABLE email_tokens;
REVOKE UPDATE (email_verified_at, password_hash) ON users FROM ce_app;
GRANT UPDATE ON users TO ce_app;
ALTER TABLE users DROP COLUMN email_verified_at;
