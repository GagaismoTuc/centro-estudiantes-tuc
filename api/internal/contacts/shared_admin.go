package contacts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PendingSharedContact holds the non-identifying fields moderated by the Platform admin.
type PendingSharedContact struct {
	ID          string
	Name        string
	HeadingName string
	Zone        string
	CreatedAt   time.Time
}

// ListPendingShared returns pending shared contacts ordered oldest-first without tenant or notes.
// pool must connect as the table owner (MIGRATE_DATABASE_URL), not ce_app.
func ListPendingShared(ctx context.Context, pool *pgxpool.Pool) ([]PendingSharedContact, error) {
	rows, err := pool.Query(ctx, `
		SELECT id::text, name, heading_name, zone, created_at
		FROM shared_contacts
		WHERE status = 'pending'
		ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list pending shared contacts: %w", err)
	}
	defer rows.Close()
	out := []PendingSharedContact{}
	for rows.Next() {
		var item PendingSharedContact
		if err := rows.Scan(&item.ID, &item.Name, &item.HeadingName, &item.Zone, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan pending shared contact: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending shared contacts: %w", err)
	}
	return out, nil
}

type sharedTransition struct {
	requiredFrom string
	targetStatus string
}

var (
	approveTransition = sharedTransition{requiredFrom: "pending", targetStatus: "approved"}
	rejectTransition  = sharedTransition{requiredFrom: "pending", targetStatus: "rejected"}
	removeTransition  = sharedTransition{requiredFrom: "approved", targetStatus: "removed"}
)

// ApproveShared transitions a pending shared contact to approved (idempotent if already approved).
func ApproveShared(ctx context.Context, pool *pgxpool.Pool, id string, now time.Time) error {
	return applySharedTransition(ctx, pool, id, approveTransition, now)
}

// RejectShared transitions a pending shared contact to rejected (idempotent if already rejected).
func RejectShared(ctx context.Context, pool *pgxpool.Pool, id string, now time.Time) error {
	return applySharedTransition(ctx, pool, id, rejectTransition, now)
}

// RemoveShared transitions an approved shared contact to removed (idempotent if already removed).
func RemoveShared(ctx context.Context, pool *pgxpool.Pool, id string, now time.Time) error {
	return applySharedTransition(ctx, pool, id, removeTransition, now)
}

func applySharedTransition(ctx context.Context, pool *pgxpool.Pool, id string, tr sharedTransition, now time.Time) error {
	if !uuidPattern.MatchString(id) {
		return fmt.Errorf("shared contact %s not found", id)
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var current string
		err := tx.QueryRow(ctx, `SELECT status FROM shared_contacts WHERE id = $1 FOR UPDATE`, id).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("shared contact %s not found", id)
		}
		if err != nil {
			return fmt.Errorf("read shared contact %s: %w", id, err)
		}
		if current == tr.targetStatus {
			return nil
		}
		if current != tr.requiredFrom {
			return fmt.Errorf("shared contact %s is not %s", id, tr.requiredFrom)
		}
		var execErr error
		if tr.targetStatus == "removed" {
			_, execErr = tx.Exec(ctx, `UPDATE shared_contacts SET status = 'removed', removed_at = $2 WHERE id = $1`, id, now.UTC())
		} else {
			_, execErr = tx.Exec(ctx, `UPDATE shared_contacts SET status = $2, reviewed_at = $3 WHERE id = $1`, id, tr.targetStatus, now.UTC())
		}
		if execErr != nil {
			return fmt.Errorf("update shared contact %s: %w", id, execErr)
		}
		return nil
	})
}
