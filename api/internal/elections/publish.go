package elections

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// publish stores, the first time, the publication of election id of tenant whose result r result
// just counted under the election's row lock: published_at = ends_at, the winner list (if any), the
// end of its commission's mandate and, when a list won, the Board handover (see handOver). It changes
// nothing when the election is already published or annulled, so of two concurrent reads, which
// queue on the row lock, only the first hands over (#58 S5). Everything is written in the caller's
// transaction: a failure leaves no half-replaced Board.
func (s *Store) publish(ctx context.Context, tx pgx.Tx, tenant, id string, r Result) error {
	var winner *string
	if r.Winner != nil {
		winner = &r.Winner.ID
	}
	var commission string
	err := tx.QueryRow(ctx, `
		UPDATE elections SET published_at = ends_at, winner_list_id = $3
		WHERE tenant_id = $1 AND id = $2 AND published_at IS NULL AND annulled_at IS NULL
		RETURNING commission_id::text`, tenant, id, winner).Scan(&commission)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE electoral_commissions SET ended_at = $3 WHERE tenant_id = $1 AND id = $2 AND ended_at IS NULL`,
		tenant, commission, s.stamp()); err != nil {
		return err
	}
	if winner == nil {
		return nil // a tie or no winner leaves the Board as it was (#58 S4)
	}
	return s.handOver(ctx, tx, tenant, id, *winner)
}

// handOver makes the candidates of list the tenant's Board with the positions of election, and
// nobody else an Officer: it removes every position and every legacy officer membership of the
// tenant, then gives each candidate the position they ran for. Student memberships stay, so an
// outgoing Officer keeps voting as a Student (ADR 0002); a candidate without an account holds the
// position on their roster entry until they claim it (#58 S3).
func (s *Store) handOver(ctx context.Context, tx pgx.Tx, tenant, election, list string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM officer_positions WHERE tenant_id = $1`, tenant); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE tenant_id = $1 AND role = 'officer'`, tenant); err != nil {
		return err
	}
	if s.handoverFault != nil {
		if err := s.handoverFault(); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO officer_positions (tenant_id, roster_member_id, position)
		SELECT c.tenant_id, c.candidate_id, p.name
		FROM election_list_candidates c
		JOIN election_positions p ON p.tenant_id = c.tenant_id AND p.election_id = c.election_id AND p.position_order = c.position_order
		WHERE c.tenant_id = $1 AND c.election_id = $2 AND c.list_id = $3`, tenant, election, list)
	return err
}

// publishClosed publishes the tenant's election in progress once its end has passed at the store's
// clock, as a read of its result would (see result): there is no background process, so whatever
// needs the tenant past that election (a new election, a new commission) publishes it first. When
// the database clock is still before the end, the election stays in progress.
func (s *Store) publishClosed(ctx context.Context, tx pgx.Tx, tenant string) error {
	var id string
	err := tx.QueryRow(ctx, `
		SELECT id::text FROM elections
		WHERE tenant_id = $1 AND published_at IS NULL AND annulled_at IS NULL AND ends_at <= $2`, tenant, s.now()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := s.result(ctx, tx, tenant, id); err != nil && !errors.Is(err, ErrResultsNotAvailable) {
		return err
	}
	return nil
}
