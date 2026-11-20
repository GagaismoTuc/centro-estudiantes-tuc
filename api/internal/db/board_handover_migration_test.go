package db_test

import (
	"context"
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

func wantPgError(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("err = %v, want SQLSTATE %s", err, code)
	}
}

// Migration 00038 (#58): a Board position follows the election position rule (trimmed, 1 to 120
// characters), and an election's winner is a list of that same election and tenant.
func TestBoardHandoverMigrationConstraints(t *testing.T) {
	ctx := integration(t)
	a := seedElectionList(t, ctx, env.tenantA)
	b := seedElectionList(t, ctx, env.tenantB)
	// Another election of the same tenant: the first one is published and its commission ended first.
	if _, err := env.super.Exec(ctx, `UPDATE elections SET published_at = ends_at WHERE id = $1`, a.election); err != nil {
		t.Fatal(err)
	}
	if _, err := env.super.Exec(ctx, `UPDATE electoral_commissions SET ended_at = now() WHERE id = $1`, a.commission); err != nil {
		t.Fatal(err)
	}
	other := seedElectionList(t, ctx, env.tenantA)
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `UPDATE elections SET winner_list_id = NULL WHERE id IN ($1, $2)`, a.election, other.election)
		_, _ = env.super.Exec(context.Background(), `DELETE FROM officer_positions WHERE roster_member_id = $1`, a.candidateID)
	})

	for _, pos := range []string{"   ", strings.Repeat("a", 121)} {
		_, err := env.super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, $3)`,
			env.tenantA, a.candidateID, pos)
		wantPgError(t, err, "23514")
	}
	if _, err := env.super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, $3)`,
		env.tenantA, a.candidateID, strings.Repeat("a", 120)); err != nil {
		t.Fatalf("a position of 120 characters: %v", err)
	}
	if _, err := env.super.Exec(ctx, `DELETE FROM officer_positions WHERE roster_member_id = $1`, a.candidateID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, 'Vocal')`,
		env.tenantA, a.candidateID); err != nil {
		t.Fatalf("a position outside the four default names: %v", err)
	}

	for _, tc := range []struct{ name, election, list string }{
		{"a list of another tenant", a.election, b.listID},
		{"a list of another election", a.election, other.listID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.super.Exec(ctx, `UPDATE elections SET winner_list_id = $2 WHERE id = $1`, tc.election, tc.list)
			wantPgError(t, err, "23503")
		})
	}
	if _, err := env.super.Exec(ctx, `UPDATE elections SET winner_list_id = $2 WHERE id = $1`, a.election, a.listID); err != nil {
		t.Fatalf("the election's own list: %v", err)
	}
}

func TestBoardHandoverMigrationUpAndDown(t *testing.T) {
	ctx := integration(t)
	if _, err := env.super.Exec(ctx, `CREATE DATABASE board_handover_migration_check`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(env.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/board_handover_migration_check?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port)
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	state := func() (column bool, check string) {
		t.Helper()
		if err := sqlDB.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT FROM information_schema.columns WHERE table_name = 'elections' AND column_name = 'winner_list_id'),
			       (SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'officer_positions_position_check')`).Scan(&column, &check); err != nil {
			t.Fatal(err)
		}
		return column, check
	}
	if _, err := p.UpTo(ctx, 36); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 38); err != nil {
		t.Fatalf("up: %v", err)
	}
	if column, check := state(); !column || !strings.Contains(check, "btrim") {
		t.Fatalf("after up: winner_list_id %t, check %q; want the column and the length rule", column, check)
	}
	if _, err := p.DownTo(ctx, 36); err != nil {
		t.Fatalf("down: %v", err)
	}
	if column, check := state(); column || !strings.Contains(check, "Tesorero") {
		t.Fatalf("after down: winner_list_id %t, check %q; want no column and the four names", column, check)
	}
}
