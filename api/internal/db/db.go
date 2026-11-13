// Package db holds the PostgreSQL access helpers that enforce tenant isolation.
package db

import (
	"context"
	"database/sql"
	"encoding/hex"
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

// ErrInvalidUserID is returned by WithUser when the user id is not a UUID.
var ErrInvalidUserID = errors.New("db: user id must be a UUID")

// ErrInvalidCodeHash is returned by WithCodeHash when the hash is not 32 bytes (HMAC-SHA256).
var ErrInvalidCodeHash = errors.New("db: code hash must be 32 bytes")

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
		if err := SetTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		return fn(tx)
	})
}

// SetTenant declares tenantID in the transaction-local app.tenant_id of tx. Besides WithTenant, only
// code running inside WithCodeHash may call it, and only with the tenant of the code row it just
// found: that is how claiming a code may write the new membership.
func SetTenant(ctx context.Context, tx pgx.Tx, tenantID string) error {
	if !uuidPattern.MatchString(tenantID) {
		return ErrInvalidTenantID
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return fmt.Errorf("db: set tenant: %w", err)
	}
	return nil
}

// WithCodeHash runs fn inside a transaction that declares hash (HMAC of an access code) through the
// transaction-local app.code_hash setting. The access_codes_holder_* policies then expose that one
// code row, for SELECT and UPDATE only, so a code can be looked up before any tenant is known. Only
// WithCodeHash writes app.code_hash.
func WithCodeHash(ctx context.Context, pool *pgxpool.Pool, hash []byte, fn func(pgx.Tx) error) error {
	if len(hash) != 32 {
		return ErrInvalidCodeHash
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.code_hash', $1, true)`, hex.EncodeToString(hash)); err != nil {
			return fmt.Errorf("db: set code hash: %w", err)
		}
		return fn(tx)
	})
}

// WithUser runs fn inside a transaction that declares userID through the transaction-local
// app.user_id setting. Its only use is reading a user's own memberships across tenants before a
// tenant is chosen (sign-in): the memberships_own_user policy is SELECT-only and matches nothing
// else. Only WithUser writes app.user_id, as only WithTenant writes app.tenant_id.
func WithUser(ctx context.Context, pool *pgxpool.Pool, userID string, fn func(pgx.Tx) error) error {
	if !uuidPattern.MatchString(userID) {
		return ErrInvalidUserID
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userID); err != nil {
			return fmt.Errorf("db: set user: %w", err)
		}
		return fn(tx)
	})
}

// CheckAppRole fails unless the pool's role is subject to Row-Level Security: a member of ce_app
// without SUPERUSER or BYPASSRLS. The server calls it at startup so a misconfigured DSN (for
// example the owner or superuser) cannot silently disable tenant isolation.
func CheckAppRole(ctx context.Context, pool *pgxpool.Pool) error {
	var name string
	var bypass, member bool
	err := pool.QueryRow(ctx, `
		SELECT rolname, rolsuper OR rolbypassrls, pg_has_role(current_user, 'ce_app', 'MEMBER')
		FROM pg_roles WHERE rolname = current_user`).Scan(&name, &bypass, &member)
	if err != nil {
		return fmt.Errorf("db: check app role: %w", err)
	}
	if bypass || !member {
		return fmt.Errorf("db: role %q must be a member of ce_app without SUPERUSER or BYPASSRLS", name)
	}
	return nil
}
