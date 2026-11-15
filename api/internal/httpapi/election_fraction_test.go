package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
)

// Owner decision on PR #91: election times are whole minutes, so a fraction of a second is
// rejected even when it is smaller than a nanosecond, the precision time.Parse keeps.

// TestReviewerFractionBeyondNanoseconds is the reviewer's test from PR #91 round 4, verbatim.
func TestReviewerFractionBeyondNanoseconds(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: needs Docker; skipped with -short")
	}
	ctx := context.Background()
	db, stop, err := testdb.Start(ctx)
	defer stop()
	if err != nil {
		t.Fatal(err)
	}
	var tenant string
	if err = db.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Review') RETURNING id::text`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Super.Exec(ctx, `INSERT INTO electoral_commissions(tenant_id) VALUES($1)`, tenant); err != nil {
		t.Fatal(err)
	}
	name := "Election"
	a, b := "2028-01-01T10:00:00Z", "2028-01-01T11:00:00Z"
	start, end := "2028-01-02T10:00:00.0000000001Z", "2028-01-02T11:00:00Z"
	d, err := (draftBody{Name: &name, SubmissionStartsAt: &a, SubmissionEndsAt: &b, StartsAt: &start, EndsAt: &end}).draft()
	if err != nil {
		t.Fatal(err)
	}
	s := elections.NewStore(db.App, func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) })
	_, err = s.CreateElection(ctx, tenant, d)
	if !errors.Is(err, elections.ErrInvalidWindow) {
		t.Errorf("got %v, want invalid_window", err)
	}
	var n int
	if err = db.Super.QueryRow(ctx, `SELECT count(*) FROM elections WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("stored elections = %d, want 0", n)
	}
}

// parseTime never turns a non-zero fraction into a zero one, and a zero fraction stays zero.
func TestParseTimeKeepsFractionsBeyondNanoseconds(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantNano int
	}{
		{"2028-01-02T10:00:00Z", 0},
		{"2028-01-02T10:00:00.0000000000Z", 0},
		{"2028-01-02T10:00:00.0000000001Z", 1},
		{"2028-01-02T10:00:00.00000000000000000009-03:00", 1},
		{"2028-01-02T10:00:00.5Z", 500000000},
		{"2028-01-02T10:00:00.0000000011+00:00", 1},
	} {
		got, ok := parseTime(&tc.in)
		if !ok {
			t.Fatalf("parseTime(%q) not ok", tc.in)
		}
		if got.Nanosecond() != tc.wantNano {
			t.Errorf("parseTime(%q).Nanosecond() = %d, want %d", tc.in, got.Nanosecond(), tc.wantNano)
		}
	}
	for _, in := range []string{"2028-01-02T10:00:00.Z", "mañana", "2028-01-02T10:00:00.1x2Z"} {
		if _, ok := parseTime(&in); ok {
			t.Errorf("parseTime(%q) ok, want malformed", in)
		}
	}
}
