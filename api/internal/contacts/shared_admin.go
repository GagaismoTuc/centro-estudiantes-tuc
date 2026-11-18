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
		// Converge cleanly if the owning school withdrew the shared contact concurrently with removal.
		if tr.targetStatus == "removed" && current == "withdrawn" {
			return nil
		}
		if current != tr.requiredFrom {
			return fmt.Errorf("shared contact %s is not %s", id, tr.requiredFrom)
		}
		utcNow := now.UTC()
		if tr.targetStatus == "approved" {
			var tenantID, contactID string
			linkErr := tx.QueryRow(ctx, `
				SELECT tenant_id::text, contact_id::text
				FROM shared_contact_links
				WHERE shared_contact_id = $1`,
				id,
			).Scan(&tenantID, &contactID)
			if linkErr != nil && !errors.Is(linkErr, pgx.ErrNoRows) {
				return fmt.Errorf("read shared contact link %s: %w", id, linkErr)
			}
			if linkErr == nil {
				oldRows, err := tx.Query(ctx, `
					SELECT sc.id::text
					FROM shared_contacts sc
					JOIN shared_contact_links l ON l.shared_contact_id = sc.id
					WHERE l.tenant_id = $1 AND l.contact_id = $2
					  AND sc.id <> $3 AND sc.status = 'approved'
					ORDER BY sc.id
					FOR UPDATE OF sc`,
					tenantID, contactID, id,
				)
				if err != nil {
					return fmt.Errorf("lock prior approved shared contacts %s: %w", id, err)
				}
				var oldIDs []string
				for oldRows.Next() {
					var oid string
					if err := oldRows.Scan(&oid); err != nil {
						oldRows.Close()
						return err
					}
					oldIDs = append(oldIDs, oid)
				}
				if err := oldRows.Err(); err != nil {
					oldRows.Close()
					return err
				}
				oldRows.Close()
				if len(oldIDs) > 0 {
					if _, err := tx.Exec(ctx, `
						UPDATE shared_contacts
						SET status = 'removed', removed_at = COALESCE(removed_at, $2)
						WHERE id = ANY($1::uuid[])`,
						oldIDs, utcNow,
					); err != nil {
						return fmt.Errorf("replace prior approved shared contacts %s: %w", id, err)
					}
				}
			}
		}
		var execErr error
		if tr.targetStatus == "removed" {
			_, execErr = tx.Exec(ctx, `UPDATE shared_contacts SET status = 'removed', removed_at = $2 WHERE id = $1`, id, utcNow)
		} else {
			_, execErr = tx.Exec(ctx, `UPDATE shared_contacts SET status = $2, reviewed_at = $3 WHERE id = $1`, id, tr.targetStatus, utcNow)
		}
		if execErr != nil {
			return fmt.Errorf("update shared contact %s: %w", id, execErr)
		}
		return nil
	})
}
