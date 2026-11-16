-- +goose Up
CREATE UNIQUE INDEX finance_categories_normalized_name ON finance_categories (tenant_id, direction, lower(btrim(name)));
GRANT UPDATE, DELETE ON finance_categories TO ce_app;

-- +goose Down
REVOKE UPDATE, DELETE ON finance_categories FROM ce_app;
DROP INDEX finance_categories_normalized_name;
