package db_test

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
)

// Migration 00016: an election is annulled exactly when it has a reason of 1 to 500 characters.
func TestElectionAnnulmentNeedsAReason(t *testing.T) {
	ctx := integration(t)
	a := seedElection(t, ctx, env.tenantA)
	for _, tc := range []struct{ name, set string }{
		{"annulled without a reason", `annulled_at = now()`},
		{"a reason without being annulled", `annulment_reason = 'Motivo'`},
		{"a blank reason", `annulled_at = now(), annulment_reason = '   '`},
		{"a reason of 501 characters", `annulled_at = now(), annulment_reason = repeat('ñ', 501)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// As superuser so only the CHECKs decide.
			_, err := env.super.Exec(ctx, `UPDATE elections SET `+tc.set+` WHERE id = $1`, a.election)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Fatalf("err = %v, want check_violation (23514)", err)
			}
		})
	}
	if _, err := env.super.Exec(ctx, `UPDATE elections SET annulled_at = now(), annulment_reason = $2 WHERE id = $1`,
		a.election, strings.Repeat("ñ", 500)); err != nil {
		t.Fatalf("a reason of 500 characters: %v", err)
	}
}

func TestElectionAnnulmentMigrationReverses(t *testing.T) {
	ctx := integration(t)
	if _, err := env.super.Exec(ctx, `CREATE DATABASE annulment_migration_check`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(env.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/annulment_migration_check?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port)
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	hasColumn := func() bool {
		t.Helper()
		var ok bool
		if err := sqlDB.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM information_schema.columns
			WHERE table_name = 'elections' AND column_name = 'annulment_reason')`).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if _, err := p.UpTo(ctx, 16); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !hasColumn() {
		t.Fatal("after up: no annulment_reason column")
	}
	if _, err := p.DownTo(ctx, 15); err != nil {
		t.Fatalf("down: %v", err)
	}
	var constraints int
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint WHERE conname LIKE 'elections_annulment_%'`).Scan(&constraints); err != nil {
		t.Fatal(err)
	}
	if hasColumn() || constraints != 0 {
		t.Fatalf("after down: column %v, %d annulment constraints; want neither", hasColumn(), constraints)
	}
	if _, err := p.UpTo(ctx, 16); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !hasColumn() {
		t.Fatal("after up again: no annulment_reason column")
	}
}
