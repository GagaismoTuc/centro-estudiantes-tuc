-- +goose Up
-- Recovery codes are now stored as HMAC-SHA256 under a key derived from TOTP_ENC_KEY (HKDF-SHA256,
-- info "recovery-codes") instead of plain SHA-256, so a database copy alone does not allow testing
-- the 50-bit codes offline. Existing SHA-256 rows can never match again: delete them (there is no
-- production data yet). Users with a confirmed TOTP keep it; for new recovery codes an operator runs
-- `admin reset-mfa` and the user enrolls again.
DELETE FROM recovery_codes;

-- Redeem now rotates the session instead of switching its tenant in place: drop the unused grant.
REVOKE UPDATE (tenant_id) ON sessions FROM ce_app;

-- +goose Down
-- The deleted codes were single-use secrets shown once: nothing to restore.
GRANT UPDATE (tenant_id) ON sessions TO ce_app;
