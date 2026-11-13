// Package db holds the PostgreSQL access helpers that enforce tenant isolation.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver used by goose
	"github.com/pressly/goose/v3"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
)

// ErrInvalidTenantID is returned by WithTenant when the tenant id is not a UUID.
var ErrInvalidTenantID = errors.New("db: tenant id must be a UUID")

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Migrate applies every pending embedded migration. Run it with a role that owns the schema,
// never with the application role.
func Migrate(ctx context.Context, dsn string) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("db: open: %w", err)
	}
	defer sqlDB.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return fmt.Errorf("db: goose provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	return nil
}

// WithTenant runs fn inside a transaction scoped to tenantID. Row-Level Security policies read
// the tenant from the app.tenant_id setting, so every tenant-owned query must go through here.
//
// The setting is transaction-local (set_config(..., true), equivalent to SET LOCAL): it vanishes
// at COMMIT or ROLLBACK, so a pooled connection never carries one tenant into the next
// transaction. This also keeps it correct behind PgBouncer in transaction mode, where consecutive
// transactions of one client may land on different server connections and session-level SET
// would leak between clients.
//
// The transaction commits when fn returns nil and rolls back otherwise. tenantID is validated
// as a UUID before any connection is used.
func WithTenant(ctx context.Context, pool *pgxpool.Pool, tenantID string, fn func(pgx.Tx) error) error {
	if !uuidPattern.MatchString(tenantID) {
		return ErrInvalidTenantID
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
			return fmt.Errorf("db: set tenant: %w", err)
		}
		return fn(tx)
	})
}
