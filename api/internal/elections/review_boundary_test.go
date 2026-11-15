package elections_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
)

// S6 (owner decision on PR #91): a submission period that ends exactly when voting starts does not
// end before the start, so the draft is rejected and nothing is stored.
func TestReviewerSubmissionEndsAtVotingStart(t *testing.T) {
	ctx := context.Background()
	db, stop, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	var tenant string
	err = db.Super.QueryRow(ctx, `INSERT INTO organizations (kind,name) VALUES ('school','Review') RETURNING id::text`).Scan(&tenant)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Super.Exec(ctx, `INSERT INTO electoral_commissions (tenant_id) VALUES ($1)`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := now.Add(48 * time.Hour)
	store := elections.NewStore(db.App, func() time.Time { return now })
	_, err = store.CreateElection(ctx, tenant, elections.Draft{Name: "Election", Mode: "secret", SubmissionStartsAt: now.Add(24 * time.Hour), SubmissionEndsAt: start, StartsAt: start, EndsAt: start.Add(time.Hour), Positions: []string{"President"}})
	if !errors.Is(err, elections.ErrInvalidSubmissionPeriod) {
		t.Errorf("error = %v, want invalid submission period", err)
	}
	var count int
	if err := db.Super.QueryRow(ctx, `SELECT count(*) FROM elections WHERE tenant_id=$1`, tenant).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("stored elections = %d, want 0", count)
	}
}

// S6: the database itself refuses a submission period that ends at the voting start, so a write
// that skips the Go check still stores nothing; one microsecond earlier is accepted.
func TestElectionsTableRejectsSubmissionEndingAtVotingStart(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	var commission string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO electoral_commissions (tenant_id) VALUES ($1) RETURNING id::text`, s.tenant).
		Scan(&commission); err != nil {
		t.Fatal(err)
	}
	insert := func(submissionEnd time.Time) error {
		_, err := tdb.Super.Exec(ctx, `
			INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at)
			VALUES ($1, $2, 'Elección', 'secret', $3, $4, $5, $6)`,
			s.tenant, commission, example2027.SubmissionStartsAt, submissionEnd, example2027.StartsAt, example2027.EndsAt)
		return err
	}
	var pgErr *pgconn.PgError
	if err := insert(example2027.StartsAt); !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("insert with submission end == start: err = %v, want check_violation 23514", err)
	}
	if n := superCount(t, ctx, "elections", s.tenant); n != 0 {
		t.Fatalf("elections = %d, want 0", n)
	}
	if err := insert(example2027.StartsAt.Add(-time.Microsecond)); err != nil {
		t.Fatalf("insert with submission end just before start: %v", err)
	}
}
