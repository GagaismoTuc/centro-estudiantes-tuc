package finance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
)

// reportSchool is a seeded school with a Treasurer and its Categories by "direction/name".
type reportSchool struct {
	sess       auth.Session
	categories map[string]string
}

func newReportSchool(t *testing.T, ctx context.Context, env *testdb.Env, name, email string) reportSchool {
	t.Helper()
	var tenant, user, member string
	if err := env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school',$1) RETURNING id::text`, name).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, tenant); err != nil {
		t.Fatal(err)
	}
	if err := env.Super.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES ($1,'x') RETURNING id::text`, email).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := env.Super.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Ana Ficticia','123') RETURNING id::text`, tenant).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role,roster_member_id) VALUES ($1,$2,'officer',$3)`, tenant, user, member); err != nil {
		t.Fatal(err)
	}
	rows, err := env.Super.Query(ctx, `SELECT direction||'/'||name, id::text FROM finance_categories WHERE tenant_id=$1`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	school := reportSchool{sess: auth.Session{TenantID: tenant, UserID: user, MFAVerified: true}, categories: map[string]string{}}
	for rows.Next() {
		var key, id string
		if err := rows.Scan(&key, &id); err != nil {
			t.Fatal(err)
		}
		school.categories[key] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return school
}

func (s reportSchool) record(t *testing.T, ctx context.Context, store *finance.Store, category, date string, cents int64) finance.Entry {
	t.Helper()
	direction := "income"
	if _, ok := s.categories["expense/"+category]; ok {
		direction = "expense"
	}
	e, err := store.Create(ctx, s.sess, finance.NewEntry{CategoryID: s.categories[direction+"/"+category], Direction: direction, Date: date, AmountCents: cents, Description: "Movimiento ficticio"})
	if err != nil {
		t.Fatalf("create %s %s: %v", category, date, err)
	}
	return e
}

// totals maps "direction/name" to the signed total of each Category in the report.
func totals(r finance.Report) map[string]int64 {
	out := map[string]int64{}
	for _, c := range r.Totals {
		out[c.Direction+"/"+c.Name] = c.TotalCents
	}
	return out
}

func assertTotals(t *testing.T, r finance.Report, want map[string]int64) {
	t.Helper()
	got := totals(r)
	if len(got) != len(want) {
		t.Fatalf("totals=%v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("totals=%v want %v", got, want)
		}
	}
}

func TestStoreFundReport(t *testing.T) {
	ctx := context.Background()
	env, stop, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	now := time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	store := finance.NewStore(env.App, func() time.Time { return now })

	// S1 (R1): opening balance, signed totals per Category and closing balance, with inclusive bounds.
	t.Run("S1 range report", func(t *testing.T) {
		a := newReportSchool(t, ctx, env, "Escuela Uno", "s1@example.invalid")
		a.record(t, ctx, store, "Donaciones y sponsors", "2026-02-28", 1000000)
		a.record(t, ctx, store, "Ventas", "2026-03-01", 200000)
		a.record(t, ctx, store, "Alquileres", "2026-11-30", 1500000)
		a.record(t, ctx, store, "Otros gastos", "2026-12-01", 99900)
		r, err := store.Report(ctx, a.sess, "2026-03-01", "2026-11-30")
		if err != nil {
			t.Fatal(err)
		}
		if r.From != "2026-03-01" || r.To != "2026-11-30" || r.OpeningCents != 1000000 || r.ClosingCents != -300000 {
			t.Fatalf("report=%+v", r)
		}
		assertTotals(t, r, map[string]int64{"income/Ventas": 200000, "expense/Alquileres": -1500000})
		if len(r.Entries) != 2 || r.Entries[0].Date != "2026-03-01" || r.Entries[1].Date != "2026-11-30" || r.Entries[1].RecordedBy != "Ana Ficticia" || r.Entries[1].Direction != "expense" || r.Entries[1].AmountCents != 1500000 {
			t.Fatalf("entries=%+v", r.Entries)
		}
	})

	// S2 (R2): a Reversal counts in its Category with the opposite sign on the date it was recorded.
	t.Run("S2 reversal", func(t *testing.T) {
		a := newReportSchool(t, ctx, env, "Escuela Dos", "s2@example.invalid")
		reversingAt := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
		clock := func() time.Time { return reversingAt }
		local := finance.NewStore(env.App, clock)
		sale := a.record(t, ctx, local, "Ventas", "2026-03-10", 500000)
		if _, err := local.Reverse(ctx, a.sess, sale.ID); err != nil {
			t.Fatal(err)
		}
		march, err := store.Report(ctx, a.sess, "2026-03-01", "2026-03-31")
		if err != nil {
			t.Fatal(err)
		}
		assertTotals(t, march, map[string]int64{"income/Ventas": 500000})
		if march.OpeningCents != 0 || march.ClosingCents != 500000 || len(march.Entries) != 1 || march.Entries[0].ReversalID == "" {
			t.Fatalf("march=%+v", march)
		}
		november, err := store.Report(ctx, a.sess, "2026-11-01", "2026-11-30")
		if err != nil {
			t.Fatal(err)
		}
		assertTotals(t, november, map[string]int64{"income/Ventas": -500000})
		if november.OpeningCents != 500000 || november.ClosingCents != 0 || len(november.Entries) != 1 || november.Entries[0].ReversesID != sale.ID || november.Entries[0].Date != "2026-11-20" {
			t.Fatalf("november=%+v", november)
		}
		both, err := store.Report(ctx, a.sess, "2026-03-01", "2026-11-30")
		if err != nil {
			t.Fatal(err)
		}
		assertTotals(t, both, map[string]int64{"income/Ventas": 0})
		if both.OpeningCents != 0 || both.ClosingCents != 0 || len(both.Entries) != 2 {
			t.Fatalf("both=%+v", both)
		}
	})

	// S3 (R3): from after to, malformed or missing dates are an invalid range; an empty range is zero.
	t.Run("S3 invalid and empty range", func(t *testing.T) {
		a := newReportSchool(t, ctx, env, "Escuela Tres", "s3@example.invalid")
		a.record(t, ctx, store, "Ventas", "2026-02-10", 300000)
		for _, c := range [][2]string{{"2026-11-30", "2026-03-01"}, {"", "2026-03-01"}, {"2026-03-01", ""}, {"2026-3-1", "2026-11-30"}, {"2026-02-30", "2026-11-30"}, {"2026-03-01", "mañana"}} {
			if _, err := store.Report(ctx, a.sess, c[0], c[1]); !errors.Is(err, finance.ErrInvalidRange) {
				t.Fatalf("range %q..%q: got %v, want ErrInvalidRange", c[0], c[1], err)
			}
		}
		r, err := store.Report(ctx, a.sess, "2026-06-01", "2026-06-30")
		if err != nil {
			t.Fatal(err)
		}
		if r.OpeningCents != 300000 || r.ClosingCents != r.OpeningCents || len(r.Totals) != 0 || len(r.Entries) != 0 || r.Totals == nil || r.Entries == nil {
			t.Fatalf("empty=%+v", r)
		}
		single, err := store.Report(ctx, a.sess, "2026-02-10", "2026-02-10")
		if err != nil || single.OpeningCents != 0 || single.ClosingCents != 300000 {
			t.Fatalf("single day=%+v err=%v", single, err)
		}
	})

	// S7 (R7): school A's report never sums school B's Entries.
	t.Run("S7 isolation", func(t *testing.T) {
		a := newReportSchool(t, ctx, env, "Escuela A", "s7a@example.invalid")
		b := newReportSchool(t, ctx, env, "Escuela B", "s7b@example.invalid")
		a.record(t, ctx, store, "Ventas", "2026-01-15", 100000)
		a.record(t, ctx, store, "Ventas", "2026-04-15", 200000)
		b.record(t, ctx, store, "Ventas", "2026-01-15", 7000000)
		b.record(t, ctx, store, "Ventas", "2026-04-15", 8000000)
		b.record(t, ctx, store, "Viajes", "2026-04-16", 900000)
		r, err := store.Report(ctx, a.sess, "2026-03-01", "2026-11-30")
		if err != nil {
			t.Fatal(err)
		}
		assertTotals(t, r, map[string]int64{"income/Ventas": 200000})
		if r.OpeningCents != 100000 || r.ClosingCents != 300000 || len(r.Entries) != 1 {
			t.Fatalf("school A=%+v", r)
		}
		for _, c := range r.Totals {
			if c.CategoryID != a.categories["income/Ventas"] {
				t.Fatalf("school A total from another school: %+v", c)
			}
		}
	})
}
