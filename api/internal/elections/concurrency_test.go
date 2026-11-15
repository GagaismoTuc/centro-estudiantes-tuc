package elections_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
)

// officerSession adds an officer account to s (so only officers designate) and returns its verified
// session.
func officerSession(t *testing.T, ctx context.Context, s school) auth.Session {
	t.Helper()
	var user string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`,
		fmt.Sprintf("officer-%s@example.com", s.tenant)).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'officer')`, s.tenant, user); err != nil {
		t.Fatal(err)
	}
	return auth.Session{UserID: user, TenantID: s.tenant, EmailVerified: true, MFAEnrolled: true, MFAVerified: true}
}

// waitFor polls the server until a backend is waiting on a lock of type lockType ('transactionid',
// 'tuple' or 'advisory', see pg_locks) running a query that contains query, or until done fires.
// It reports whether the backend was seen waiting. Polling only decides when to go on: the outcome
// the tests check is fixed by the locks, not by timing.
func waitFor(t *testing.T, ctx context.Context, lockType, query string, done <-chan struct{}) bool {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return false
		default:
		}
		var waiting bool
		if err := tdb.Super.QueryRow(ctx, `
			SELECT EXISTS (SELECT FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
			               WHERE NOT l.granted AND l.locktype = $1 AND strpos(a.query, $2) > 0)`, lockType, query).
			Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no backend waiting on a %s lock for %q", lockType, query)
	return false
}

// S4, S8 (PR #91 review): a commission member whose write is in flight while the commission is
// replaced either writes before the replacement commits, or is refused with ErrForbidden. The
// member's write is held at a lock taken from a separate connection after its permission check, so
// the replacement runs exactly in that window.
func TestDesignateDuringADraftWriteNeverLetsARemovedMemberWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		// hold opens tx on a separate connection with the lock the member's write waits for; it
		// returns the substring of the write's statement and the lock type it waits on.
		hold  func(t *testing.T, ctx context.Context, s school, election string, tx pgx.Tx) (query, lockType string)
		write func(ctx context.Context, st *elections.Store, sess auth.Session, election string) error
		// stored reports whether the member's write is in the database.
		stored func(t *testing.T, ctx context.Context, s school, st *elections.Store, election string) bool
	}{
		{
			name: "Create",
			hold: func(t *testing.T, ctx context.Context, s school, _ string, tx pgx.Tx) (string, string) {
				// An uncommitted election in progress makes Create's INSERT wait on the
				// elections_one_in_progress index; rolling it back lets the INSERT through.
				if _, err := tx.Exec(ctx, `
					INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at)
					SELECT tenant_id, id, 'Bloqueo', 'secret', $2, $3, $4, $5 FROM electoral_commissions WHERE tenant_id = $1 AND ended_at IS NULL`,
					s.tenant, example2027.SubmissionStartsAt, example2027.SubmissionEndsAt, example2027.StartsAt, example2027.EndsAt); err != nil {
					t.Fatal(err)
				}
				return "INSERT INTO elections", "transactionid"
			},
			write: func(ctx context.Context, st *elections.Store, sess auth.Session, _ string) error {
				_, err := st.Create(ctx, sess, draft2027())
				return err
			},
			stored: func(t *testing.T, ctx context.Context, s school, _ *elections.Store, _ string) bool {
				return superCount(t, ctx, "elections", s.tenant) == 1
			},
		},
		{
			name: "Edit",
			hold: func(t *testing.T, ctx context.Context, s school, election string, tx pgx.Tx) (string, string) {
				// Edit locks the election row FOR UPDATE after its permission check.
				if _, err := tx.Exec(ctx, `SELECT FROM elections WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, s.tenant, election); err != nil {
					t.Fatal(err)
				}
				return "FOR UPDATE OF e", "transactionid"
			},
			write: func(ctx context.Context, st *elections.Store, sess auth.Session, election string) error {
				d := draft2027()
				d.Name = "Editada"
				_, err := st.Edit(ctx, sess, election, d)
				return err
			},
			stored: func(t *testing.T, ctx context.Context, s school, st *elections.Store, election string) bool {
				e, err := st.GetElection(ctx, s.tenant, election)
				if err != nil {
					t.Fatal(err)
				}
				return e.Name == "Editada"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := integration(t)
			s := newSchool(t, ctx)
			st := newStore(today())
			officer := officerSession(t, ctx, s)
			ana := auth.Session{UserID: s.anaUser, TenantID: s.tenant, EmailVerified: true, MFAEnrolled: true, MFAVerified: true}
			if _, err := st.Designate(ctx, officer, []string{s.ana}); err != nil {
				t.Fatal(err)
			}
			var election string
			if tc.name == "Edit" {
				e, err := st.Create(ctx, ana, draft2027())
				if err != nil {
					t.Fatal(err)
				}
				election = e.ID
			}

			blocker, err := tdb.Super.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background()) //nolint:errcheck // already rolled back on the happy path
			query, lockType := tc.hold(t, ctx, s, election, blocker)

			writeErr := make(chan error, 1)
			writeDone := make(chan struct{})
			go func() { writeErr <- tc.write(ctx, st, ana, election); close(writeDone) }()
			if !waitFor(t, ctx, lockType, query, writeDone) {
				t.Fatalf("%s finished without waiting on the held lock: %v", tc.name, <-writeErr)
			}
			// The write passed its permission check and now waits.

			var designateErr error
			designateDone := make(chan struct{})
			go func() { _, designateErr = st.Designate(ctx, officer, []string{s.beto}); close(designateDone) }()
			// Either the replacement commits while the write waits, or it waits for the write.
			designatedFirst := !waitFor(t, ctx, "advisory", "pg_advisory_xact_lock", designateDone)
			t.Logf("Designate committed while the %s waited: %v", tc.name, designatedFirst)

			if err := blocker.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			<-writeDone
			<-designateDone
			if designateErr != nil {
				t.Fatalf("Designate: %v", designateErr)
			}
			err = <-writeErr
			stored := tc.stored(t, ctx, s, st, election)
			if designatedFirst {
				if !errors.Is(err, elections.ErrForbidden) || stored {
					t.Fatalf("ana removed before her %s committed: err = %v, stored = %v; want ErrForbidden and nothing stored", tc.name, err, stored)
				}
			} else if err != nil || !stored {
				t.Fatalf("%s committed before the replacement: err = %v, stored = %v; want stored", tc.name, err, stored)
			}
			cur, err := st.CurrentCommission(ctx, s.tenant)
			if err != nil {
				t.Fatal(err)
			}
			if ids := memberIDs(cur); !slices.Equal(ids, []string{s.beto}) {
				t.Fatalf("commission = %v, want beto", ids)
			}
		})
	}
}
