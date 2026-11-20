package elections_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
)

// boardState is what a publication may change, read as superuser: the Board ("position:member:name"
// and "legacy:user" rows, sorted), whether the election is published and its winner, and whether its
// commission's mandate ended.
type boardState struct {
	board            []string
	published, ended bool
	winner           string
}

func readBoardState(t *testing.T, ctx context.Context, tenant, election string) boardState {
	t.Helper()
	var b boardState
	if err := tdb.Super.QueryRow(ctx, `
		SELECT (SELECT coalesce(array_agg(x ORDER BY x), '{}') FROM (
		            SELECT 'position:' || roster_member_id::text || ':' || position AS x FROM officer_positions WHERE tenant_id = $1
		            UNION ALL
		            SELECT 'legacy:' || user_id::text FROM memberships WHERE tenant_id = $1 AND role = 'officer') q),
		       e.published_at IS NOT NULL, coalesce(e.winner_list_id::text, ''), c.ended_at IS NOT NULL
		FROM elections e JOIN electoral_commissions c ON c.tenant_id = e.tenant_id AND c.id = e.commission_id
		WHERE e.tenant_id = $1 AND e.id = $2`, tenant, election).Scan(&b.board, &b.published, &b.winner, &b.ended); err != nil {
		t.Fatal(err)
	}
	return b
}

// #58 S5: removing the old Board and writing the new one happen in one transaction with the
// publication: a failure in between leaves the Board, the publication and the commission as they
// were; the next read publishes and hands over once.
func TestHandoverFailureLeavesNothingHalfDone(t *testing.T) {
	ctx := integration(t)
	clk := &fakeClock{on2025(9, 0)}
	st := newStore(clk)
	v := newVoting(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `
		INSERT INTO election_positions (tenant_id, election_id, position_order, name) VALUES ($1, $2, 1, 'Presidente'), ($1, $2, 2, 'Vocal')`,
		v.tenant, v.election); err != nil {
		t.Fatal(err)
	}
	dana := addRosterMember(t, ctx, v.tenant, "Dana Duarte", "200")
	if _, err := tdb.Super.Exec(ctx, `
		INSERT INTO election_list_candidates (tenant_id, election_id, list_id, position_order, candidate_id) VALUES ($1, $2, $3, 1, $4), ($1, $2, $3, 2, $5)`,
		v.tenant, v.election, v.azul, v.carla, dana); err != nil {
		t.Fatal(err)
	}
	officerSession(t, ctx, v.school) // a legacy Officer account, also replaced by the handover
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, 'Presidente')`,
		v.tenant, v.beto); err != nil {
		t.Fatal(err)
	}
	if err := st.CastVote(ctx, v.tenant, v.election, v.ana, v.azul); err != nil {
		t.Fatal(err)
	}
	clk.t = on2025(18, 0)
	before := readBoardState(t, ctx, v.tenant, v.election)
	if len(before.board) != 2 || before.published || before.ended {
		t.Fatalf("before = %+v, want Beto and the legacy officer, unpublished, commission current", before)
	}
	ana := auth.Session{UserID: v.anaUser, TenantID: v.tenant}

	fault := errors.New("disk full")
	elections.SetHandoverFault(st, func() error { return fault })
	if _, err := st.ViewResult(ctx, ana, v.election); !errors.Is(err, fault) {
		t.Fatalf("ViewResult with a fault = %v, want %v", err, fault)
	}
	if got := readBoardState(t, ctx, v.tenant, v.election); !slices.Equal(got.board, before.board) || got.published || got.winner != "" || got.ended {
		t.Fatalf("after the failure = %+v, want %+v", got, before)
	}

	elections.SetHandoverFault(st, nil)
	r, err := st.ViewResult(ctx, ana, v.election)
	if err != nil || r.Outcome != elections.OutcomeWinner || r.Winner.ID != v.azul {
		t.Fatalf("ViewResult = %+v, %v; want Lista Azul as winner", r, err)
	}
	want := []string{"position:" + v.carla + ":Presidente", "position:" + dana + ":Vocal"}
	slices.Sort(want)
	got := readBoardState(t, ctx, v.tenant, v.election)
	if !slices.Equal(got.board, want) || !got.published || got.winner != v.azul || !got.ended {
		t.Fatalf("after the handover = %+v, want board %v, published with %s, commission ended", got, want, v.azul)
	}
	if _, err := st.ViewResult(ctx, ana, v.election); err != nil {
		t.Fatal(err)
	}
	if again := readBoardState(t, ctx, v.tenant, v.election); !slices.Equal(again.board, got.board) || !again.published || again.winner != v.azul || !again.ended {
		t.Fatalf("after rereading = %+v, want %+v", again, got)
	}
}
