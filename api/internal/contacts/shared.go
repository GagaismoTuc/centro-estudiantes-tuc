package contacts

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
)

const sharedPageSize = 50

const accentFrom = "áàäâãéèëêíìïîóòöôõúùüû"
const accentTo = "aaaaaeeeeiiiiooooouuuu"

// ShareResult is returned when an officer shares a contact into the pending moderation queue.
type ShareResult struct {
	ID        string
	Status    string
	CreatedAt time.Time
}

// SharedReferencePrice is a dated reference price copied into a shared contact snapshot.
type SharedReferencePrice struct {
	Description string `json:"description"`
	AmountCents int64  `json:"amountCents"`
	Date        string `json:"date"`
}

// SharedContact is an approved entry from the platform-wide shared directory (no notes, no source school).
type SharedContact struct {
	ID              string
	HeadingName     string
	Name            string
	Phone           string
	Email           string
	Zone            string
	ReferencePrices []SharedReferencePrice
	CreatedAt       time.Time
}

// SharedContactPage holds one page of approved shared contacts.
type SharedContactPage struct {
	Items      []SharedContact
	Headings   []string
	Page       int
	PageSize   int
	Total      int
	TotalPages int
}

// ShareContact copies shareable fields (excluding notes and school) of contactID into a pending
// shared_contacts row linked to the active tenant. Concurrent calls for the same contact serialize
// on the contact row lock so only one succeeds.
func (s *Store) ShareContact(ctx context.Context, sess auth.Session, contactID string) (ShareResult, error) {
	var out ShareResult
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(contactID) {
			return ErrNotFound
		}

		var name, headingName, phone, email, zone string
		err := tx.QueryRow(ctx, `
			SELECT c.name, h.name, c.phone, c.email, c.zone
			FROM contacts c
			JOIN contact_headings h ON h.tenant_id = c.tenant_id AND h.id = c.heading_id
			WHERE c.tenant_id = $1 AND c.id = $2
			FOR UPDATE OF c`,
			sess.TenantID, contactID,
		).Scan(&name, &headingName, &phone, &email, &zone)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("contacts: load contact for share: %w", err)
		}

		var latestStatus string
		err = tx.QueryRow(ctx, `
			SELECT sc.status
			FROM shared_contact_links l
			JOIN shared_contacts sc ON sc.id = l.shared_contact_id
			WHERE l.tenant_id = $1 AND l.contact_id = $2
			ORDER BY l.created_at DESC, l.id DESC
			LIMIT 1`,
			sess.TenantID, contactID,
		).Scan(&latestStatus)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("contacts: check share status: %w", err)
		}
		if err == nil && (latestStatus == "pending" || latestStatus == "approved") {
			return ErrAlreadyShared
		}

		rows, err := tx.Query(ctx, `
			SELECT description, amount_cents, to_char(quoted_on, 'YYYY-MM-DD')
			FROM contact_reference_prices
			WHERE tenant_id = $1 AND contact_id = $2
			ORDER BY quoted_on DESC, created_at DESC, id`,
			sess.TenantID, contactID)
		if err != nil {
			return fmt.Errorf("contacts: load prices for share: %w", err)
		}
		prices := []SharedReferencePrice{}
		for rows.Next() {
			var rp SharedReferencePrice
			if err := rows.Scan(&rp.Description, &rp.AmountCents, &rp.Date); err != nil {
				rows.Close()
				return err
			}
			prices = append(prices, rp)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		pricesJSON, err := json.Marshal(prices)
		if err != nil {
			return fmt.Errorf("contacts: marshal shared prices: %w", err)
		}

		sharedID, err := newUUIDv4()
		if err != nil {
			return fmt.Errorf("contacts: generate shared id: %w", err)
		}
		now := s.now().UTC()
		// Insert without RETURNING because ce_app's SELECT policy checks shared_contact_links, which is inserted next.
		if _, err := tx.Exec(ctx, `
			INSERT INTO shared_contacts (id, status, heading_name, name, phone, email, zone, reference_prices, created_at)
			VALUES ($1, 'pending', $2, $3, $4, $5, $6, $7::jsonb, $8)`,
			sharedID, headingName, name, phone, email, zone, string(pricesJSON), now,
		); err != nil {
			return fmt.Errorf("contacts: insert shared contact: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO shared_contact_links (tenant_id, contact_id, shared_contact_id, created_at)
			VALUES ($1, $2, $3, $4)`,
			sess.TenantID, contactID, sharedID, now,
		); err != nil {
			return fmt.Errorf("contacts: insert shared link: %w", err)
		}

		out = ShareResult{ID: sharedID, Status: "pending", CreatedAt: now}
		return nil
	})
	return out, err
}

// ListSharedContacts returns one page (50 items) of approved shared contacts filtered by exact
// case-insensitive heading name and partial case- and accent-insensitive zone.
func (s *Store) ListSharedContacts(ctx context.Context, sess auth.Session, heading, zone string, page int) (SharedContactPage, error) {
	if page < 1 {
		return SharedContactPage{}, ErrInvalidPage
	}
	out := SharedContactPage{
		Items:    []SharedContact{},
		Headings: []string{},
		Page:     page,
		PageSize: sharedPageSize,
	}
	err := s.asReader(ctx, sess, func(tx pgx.Tx) error {
		args := []any{}
		where := []string{"status = 'approved'"}

		if h := strings.TrimSpace(heading); h != "" {
			args = append(args, h)
			where = append(where, fmt.Sprintf("lower(heading_name) = lower($%d)", len(args)))
		}
		if z := strings.TrimSpace(zone); z != "" {
			args = append(args, "%"+escapeLike(foldAccentsLower(z))+"%")
			where = append(where, fmt.Sprintf("translate(lower(zone), '%s', '%s') LIKE $%d ESCAPE '\\'", accentFrom, accentTo, len(args)))
		}
		whereSQL := strings.Join(where, " AND ")

		if err := tx.QueryRow(ctx, `SELECT count(*) FROM shared_contacts WHERE `+whereSQL, args...).Scan(&out.Total); err != nil {
			return fmt.Errorf("contacts: count shared contacts: %w", err)
		}
		if out.Total > 0 {
			out.TotalPages = (out.Total + sharedPageSize - 1) / sharedPageSize
		}

		hRows, err := tx.Query(ctx, `
			SELECT heading_name
			FROM shared_contacts
			WHERE status = 'approved'
			GROUP BY heading_name
			ORDER BY lower(heading_name), heading_name`)
		if err != nil {
			return fmt.Errorf("contacts: list shared headings: %w", err)
		}
		for hRows.Next() {
			var h string
			if err := hRows.Scan(&h); err != nil {
				hRows.Close()
				return err
			}
			out.Headings = append(out.Headings, h)
		}
		if err := hRows.Err(); err != nil {
			hRows.Close()
			return err
		}
		hRows.Close()

		offset := (page - 1) * sharedPageSize
		pageArgs := append(append([]any{}, args...), sharedPageSize, offset)
		rows, err := tx.Query(ctx, fmt.Sprintf(`
			SELECT id::text, heading_name, name, phone, email, zone, reference_prices, created_at
			FROM shared_contacts
			WHERE %s
			ORDER BY lower(name), id
			LIMIT $%d OFFSET $%d`, whereSQL, len(args)+1, len(args)+2), pageArgs...)
		if err != nil {
			return fmt.Errorf("contacts: list shared contacts: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var item SharedContact
			var pricesRaw []byte
			if err := rows.Scan(
				&item.ID, &item.HeadingName, &item.Name, &item.Phone, &item.Email, &item.Zone, &pricesRaw, &item.CreatedAt,
			); err != nil {
				return err
			}
			item.ReferencePrices = []SharedReferencePrice{}
			if len(pricesRaw) > 0 {
				if err := json.Unmarshal(pricesRaw, &item.ReferencePrices); err != nil {
					return fmt.Errorf("contacts: unmarshal shared prices: %w", err)
				}
			}
			out.Items = append(out.Items, item)
		}
		return rows.Err()
	})
	return out, err
}

func foldAccentsLower(s string) string {
	r := strings.NewReplacer(
		"á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a",
		"é", "e", "è", "e", "ë", "e", "ê", "e",
		"í", "i", "ì", "i", "ï", "i", "î", "i",
		"ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o",
		"ú", "u", "ù", "u", "ü", "u", "û", "u",
	)
	return r.Replace(strings.ToLower(s))
}

func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
