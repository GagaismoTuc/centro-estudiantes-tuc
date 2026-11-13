-- +goose Up
-- Claiming an account happens before any tenant is known. Whoever holds a code may see and consume
-- that one row: db.WithCodeHash declares HMAC(code) in the transaction-local app.code_hash, and these
-- policies match only the row with that hash. No SECURITY DEFINER, no RLS bypass. Fails closed: an
-- unset or empty setting gives decode(NULL) = NULL, which matches nothing. SELECT and UPDATE only, so
-- a code hash can never be used to INSERT (or DELETE, which ce_app is not granted) codes.
CREATE POLICY access_codes_holder_select ON access_codes
    FOR SELECT
    TO ce_app
    USING (code_hash = decode(nullif(current_setting('app.code_hash', true), ''), 'hex'));

CREATE POLICY access_codes_holder_update ON access_codes
    FOR UPDATE
    TO ce_app
    USING (code_hash = decode(nullif(current_setting('app.code_hash', true), ''), 'hex'))
    WITH CHECK (code_hash = decode(nullif(current_setting('app.code_hash', true), ''), 'hex'));

-- Redeeming a code switches the session's active tenant to the code's school.
GRANT UPDATE (tenant_id) ON sessions TO ce_app;

-- +goose Down
REVOKE UPDATE (tenant_id) ON sessions FROM ce_app;
DROP POLICY access_codes_holder_update ON access_codes;
DROP POLICY access_codes_holder_select ON access_codes;
