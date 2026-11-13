-- +goose Up
-- The app only ever marks codes used or revoked. A table-wide UPDATE grant let a code holder (or any
-- tenant transaction) rewrite tenant_id / roster_member_id and move a code to another tenant.
REVOKE UPDATE ON access_codes FROM ce_app;
GRANT UPDATE (used_at, revoked_at) ON access_codes TO ce_app;

-- +goose Down
REVOKE UPDATE (used_at, revoked_at) ON access_codes FROM ce_app;
GRANT UPDATE ON access_codes TO ce_app;
