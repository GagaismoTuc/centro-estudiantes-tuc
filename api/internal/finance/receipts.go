package finance

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/jackc/pgx/v5"
)

// MaxReceiptBytes is the inclusive maximum size of an uploaded Receipt (10 MiB).
const MaxReceiptBytes = 10 << 20

var (
	ErrEmptyFile            = errors.New("finance: empty file")
	ErrFileTooLarge         = errors.New("finance: file too large")
	ErrUnsupportedMediaType = errors.New("finance: unsupported media type")
)

var (
	jpgMagic = []byte{0xFF, 0xD8, 0xFF}
	pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	pdfMagic = []byte("%PDF-")
)

// Receipt holds metadata (and Data when fetched for download) of a stored Comprobante.
type Receipt struct {
	ID          string
	ContentType string
	SizeBytes   int
	UploadedAt  time.Time
	Data        []byte
}

// DetectReceiptType validates receipt size and identifies JPG, PNG, or PDF solely from leading
// file-signature bytes, ignoring declared filenames and Content-Type headers.
func DetectReceiptType(data []byte) (string, error) {
	if len(data) == 0 {
		return "", ErrEmptyFile
	}
	if len(data) > MaxReceiptBytes {
		return "", ErrFileTooLarge
	}
	switch {
	case bytes.HasPrefix(data, jpgMagic):
		return "image/jpeg", nil
	case bytes.HasPrefix(data, pngMagic):
		return "image/png", nil
	case bytes.HasPrefix(data, pdfMagic):
		return "application/pdf", nil
	default:
		return "", ErrUnsupportedMediaType
	}
}

// ReceiptExt returns the canonical download extension for a validated receipt MIME type.
func ReceiptExt(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "application/pdf":
		return "pdf"
	default:
		return "bin"
	}
}

func (s *Store) withReceiptReader(ctx context.Context, sess auth.Session, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" || sess.UserID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		var allowed bool
		err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM memberships m
				WHERE m.tenant_id = $1 AND m.user_id = $2
				  AND (
					  m.role IN ('school_admin', 'officer', 'veedor')
					  OR EXISTS (
						  SELECT 1 FROM officer_positions p
						  WHERE p.tenant_id = m.tenant_id AND p.roster_member_id = m.roster_member_id
					  )
				  )
			)`, sess.TenantID, sess.UserID).Scan(&allowed)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrForbidden
		}
		if err := auth.RequireMFA(sess); err != nil {
			return err
		}
		return fn(tx)
	})
}

func ensureEntryExists(ctx context.Context, tx pgx.Tx, tenantID, entryID string) error {
	if !uuidPattern.MatchString(entryID) {
		return ErrNotFound
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_entries WHERE tenant_id=$1 AND id=$2)`, tenantID, entryID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// AddReceipt validates and stores a Receipt for entryID inside one tenant-scoped transaction.
func (s *Store) AddReceipt(ctx context.Context, sess auth.Session, entryID string, data []byte) (Receipt, error) {
	var out Receipt
	err := s.withMember(ctx, sess, true, func(tx pgx.Tx) error {
		if err := ensureEntryExists(ctx, tx, sess.TenantID, entryID); err != nil {
			return err
		}
		contentType, err := DetectReceiptType(data)
		if err != nil {
			return err
		}
		uploadedAt := s.now()
		out = Receipt{
			ContentType: contentType,
			SizeBytes:   len(data),
		}
		return tx.QueryRow(ctx,
			`INSERT INTO finance_receipts (tenant_id, entry_id, content_type, size_bytes, data, uploaded_by, uploaded_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING id::text, uploaded_at`,
			sess.TenantID, entryID, contentType, len(data), data, sess.UserID, uploadedAt,
		).Scan(&out.ID, &out.UploadedAt)
	})
	if err != nil {
		return Receipt{}, err
	}
	return out, nil
}

// ListReceipts returns metadata of all Receipts attached to entryID for authorized readers.
func (s *Store) ListReceipts(ctx context.Context, sess auth.Session, entryID string) ([]Receipt, error) {
	out := []Receipt{}
	err := s.withReceiptReader(ctx, sess, func(tx pgx.Tx) error {
		if err := ensureEntryExists(ctx, tx, sess.TenantID, entryID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`SELECT id::text, content_type, size_bytes, uploaded_at
			 FROM finance_receipts
			 WHERE tenant_id=$1 AND entry_id=$2
			 ORDER BY uploaded_at ASC, id ASC`,
			sess.TenantID, entryID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r Receipt
			if err := rows.Scan(&r.ID, &r.ContentType, &r.SizeBytes, &r.UploadedAt); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetReceipt returns one Receipt with its bytes when it belongs to entryID in the active tenant.
func (s *Store) GetReceipt(ctx context.Context, sess auth.Session, entryID, receiptID string) (Receipt, error) {
	var out Receipt
	err := s.withReceiptReader(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(entryID) || !uuidPattern.MatchString(receiptID) {
			return ErrNotFound
		}
		err := tx.QueryRow(ctx,
			`SELECT id::text, content_type, size_bytes, data, uploaded_at
			 FROM finance_receipts
			 WHERE tenant_id=$1 AND entry_id=$2 AND id=$3`,
			sess.TenantID, entryID, receiptID,
		).Scan(&out.ID, &out.ContentType, &out.SizeBytes, &out.Data, &out.UploadedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return Receipt{}, err
	}
	return out, nil
}
