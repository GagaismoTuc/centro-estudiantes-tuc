package roster

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
)

var (
	ErrAlreadyOfficer  = errors.New("roster: already officer")
	ErrPositionTaken   = errors.New("roster: position taken")
	ErrInvalidPosition = errors.New("roster: invalid position")
	// ErrBoardElected means the tenant's latest published election had a winner, whose list is the
	// Board: positions are no longer assigned or removed by hand (#58 S7).
	ErrBoardElected = errors.New("roster: board elected")
)

// boardElected returns ErrBoardElected when the latest published, non-annulled election of tenant
// had a winner. It first takes a share lock on the tenant's elections, so it waits for a publication
// in progress (which holds its election's row lock while it hands the Board over) and then reads
// what it committed: a manual change never lands on top of a handover.
func boardElected(ctx context.Context, tx pgx.Tx, tenant string) error {
	if _, err := tx.Exec(ctx, `SELECT FROM elections WHERE tenant_id = $1 FOR SHARE`, tenant); err != nil {
		return err
	}
	var elected bool
	if err := tx.QueryRow(ctx, `
		SELECT coalesce((SELECT winner_list_id IS NOT NULL FROM elections
		                 WHERE tenant_id = $1 AND published_at IS NOT NULL AND annulled_at IS NULL
		                 ORDER BY published_at DESC, ends_at DESC LIMIT 1), false)`, tenant).Scan(&elected); err != nil {
		return err
	}
	if elected {
		return ErrBoardElected
	}
	return nil
}

type SchoolMember struct {
	Email, FullName, RosterMemberID, Position *string
	Roles                                     []string
}

func (s *Store) ListMembers(ctx context.Context, sess auth.Session) ([]SchoolMember, error) {
	out := []SchoolMember{}
	err := s.asAdmin(ctx, sess, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT u.email, rm.full_name, rm.id::text, p.position,
			       ARRAY(SELECT role FROM memberships x WHERE x.tenant_id=$1 AND x.user_id=u.id
			             UNION SELECT 'officer' WHERE p.roster_member_id IS NOT NULL)
			FROM users u JOIN (SELECT DISTINCT user_id FROM memberships WHERE tenant_id=$1) a ON a.user_id=u.id
			LEFT JOIN LATERAL (SELECT roster_member_id FROM memberships WHERE tenant_id=$1 AND user_id=u.id AND roster_member_id IS NOT NULL LIMIT 1) l ON true
			LEFT JOIN roster_members rm ON rm.tenant_id=$1 AND rm.id=l.roster_member_id
			LEFT JOIN officer_positions p ON p.tenant_id=$1 AND p.roster_member_id=rm.id
			UNION ALL
			SELECT NULL, rm.full_name, rm.id::text, p.position, ARRAY['student','officer']
			FROM officer_positions p JOIN roster_members rm ON rm.tenant_id=p.tenant_id AND rm.id=p.roster_member_id
			WHERE p.tenant_id=$1 AND NOT EXISTS (SELECT FROM memberships m WHERE m.tenant_id=p.tenant_id AND m.roster_member_id=p.roster_member_id)`, sess.TenantID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[SchoolMember])
		return err
	})
	return out, err
}

func (s *Store) AssignOfficer(ctx context.Context, sess auth.Session, member, position string) error {
	return s.asAdmin(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(member) {
			return ErrNotFound
		}
		switch position {
		case "Presidente", "Vicepresidente", "Secretario", "Tesorero":
		default:
			return ErrInvalidPosition
		}
		if err := boardElected(ctx, tx, sess.TenantID); err != nil {
			return err
		}
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM roster_members WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, sess.TenantID, member).Scan(&locked); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM officer_positions WHERE tenant_id=$1 AND roster_member_id=$2)
			OR EXISTS (SELECT FROM memberships linked JOIN memberships officer ON officer.tenant_id=linked.tenant_id AND officer.user_id=linked.user_id AND officer.role='officer'
			WHERE linked.tenant_id=$1 AND linked.roster_member_id=$2)`, sess.TenantID, member).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrAlreadyOfficer
		}
		_, err := tx.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1,$2,$3)`, sess.TenantID, member, position)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "officer_positions_pkey" {
				return ErrAlreadyOfficer
			}
			if pgErr.ConstraintName == "officer_positions_tenant_id_position_key" {
				return ErrPositionTaken
			}
		}
		return err
	})
}

func (s *Store) RemoveOfficer(ctx context.Context, sess auth.Session, member string) error {
	return s.asAdmin(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(member) {
			return ErrNotFound
		}
		if err := boardElected(ctx, tx, sess.TenantID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM officer_positions WHERE tenant_id=$1 AND roster_member_id=$2`, sess.TenantID, member)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}
