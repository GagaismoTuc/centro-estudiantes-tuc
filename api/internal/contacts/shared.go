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

var accentReplacer = buildAccentReplacer(accentFrom, accentTo)

func buildAccentReplacer(from, to string) *strings.Replacer {
	fromRunes := []rune(from)
	toRunes := []rune(to)
	pairs := make([]string, 0, len(fromRunes)*2)
	for i, r := range fromRunes {
		pairs = append(pairs, string(r), string(toRunes[i]))
	}
	return strings.NewReplacer(pairs...)
}

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

		var hasPending bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM shared_contact_links l
				JOIN shared_contacts sc ON sc.id = l.shared_contact_id
				WHERE l.tenant_id = $1 AND l.contact_id = $2 AND sc.status = 'pending'
			)`,
			sess.TenantID, contactID,
		).Scan(&hasPending)
		if err != nil {
			return fmt.Errorf("contacts: check share status: %w", err)
		}
		if hasPending {
			return ErrAlreadyPending
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

// WithdrawShare withdraws all active (pending or approved) share requests for contactID owned
// by the active tenant. Returns ErrNotFound if contactID does not belong to the active tenant,
// and ErrNotShared if the contact has no pending or approved share request.
func (s *Store) WithdrawShare(ctx context.Context, sess auth.Session, contactID string) error {
	return s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(contactID) {
			return ErrNotFound
		}
		var exists int
		err := tx.QueryRow(ctx, `SELECT 1 FROM contacts WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
			sess.TenantID, contactID).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("contacts: lock contact for withdraw: %w", err)
		}

		rows, err := tx.Query(ctx, `
			SELECT l.shared_contact_id::text
			FROM shared_contact_links l
			JOIN shared_contacts sc ON sc.id = l.shared_contact_id
			WHERE l.tenant_id = $1 AND l.contact_id = $2
			  AND sc.status IN ('pending', 'approved')
			ORDER BY l.seq DESC`,
			sess.TenantID, contactID,
		)
		if err != nil {
			return fmt.Errorf("contacts: list active shares for withdraw: %w", err)
		}
		var activeIDs []string
		for rows.Next() {
			var scID string
			if err := rows.Scan(&scID); err != nil {
				rows.Close()
				return err
			}
			activeIDs = append(activeIDs, scID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		if len(activeIDs) == 0 {
			return ErrNotShared
		}
		now := s.now().UTC()
		for _, scID := range activeIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO shared_contact_withdrawals (tenant_id, shared_contact_id, created_at)
				VALUES ($1, $2, $3)
				ON CONFLICT (shared_contact_id) DO NOTHING`,
				sess.TenantID, scID, now,
			); err != nil {
				return fmt.Errorf("contacts: insert withdrawal: %w", err)
			}
		}
		return nil
	})
}

// CopySharedContact copies an approved entry from the shared directory into the active school's
// own contact directory as an independent contact (without notes or source school) in a single
// transaction. Matching active headings are resolved case-insensitively, falling back to "otros".
func (s *Store) CopySharedContact(ctx context.Context, sess auth.Session, sharedContactID string) (Contact, error) {
	var out Contact
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(sharedContactID) {
			return ErrNotFound
		}
		var headingName, name, phone, email, zone string
		var pricesRaw []byte
		err := tx.QueryRow(ctx, `
			SELECT heading_name, name, phone, email, zone, reference_prices
			FROM shared_contacts
			WHERE id = $1 AND status = 'approved'`,
			sharedContactID,
		).Scan(&headingName, &name, &phone, &email, &zone, &pricesRaw)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("contacts: load shared contact for copy: %w", err)
		}

		prices := []SharedReferencePrice{}
		if len(pricesRaw) > 0 {
			if err := json.Unmarshal(pricesRaw, &prices); err != nil {
				return fmt.Errorf("contacts: unmarshal shared prices for copy: %w", err)
			}
		}

		headingID, err := resolveCopyHeadingID(ctx, tx, sess.TenantID, headingName)
		if err != nil {
			return err
		}

		now := s.now().UTC()
		var newID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO contacts (tenant_id, heading_id, name, phone, email, zone, notes, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, '', $7, $7)
			RETURNING id::text`,
			sess.TenantID, headingID, name, phone, email, zone, now,
		).Scan(&newID); err != nil {
			return fmt.Errorf("contacts: insert copied contact: %w", err)
		}

		for i, rp := range prices {
			// Offset created_at by microseconds so tie-breaking preserves source order on equal dates.
			priceTime := now.Add(-time.Duration(i) * time.Microsecond)
			if _, err := tx.Exec(ctx, `
				INSERT INTO contact_reference_prices (tenant_id, contact_id, description, amount_cents, quoted_on, created_at)
				VALUES ($1, $2, $3, $4, $5::date, $6)`,
				sess.TenantID, newID, rp.Description, rp.AmountCents, rp.Date, priceTime,
			); err != nil {
				return fmt.Errorf("contacts: insert copied reference price: %w", err)
			}
		}

		out, err = loadContact(ctx, tx, sess.TenantID, newID)
		return err
	})
	return out, err
}

func resolveCopyHeadingID(ctx context.Context, tx pgx.Tx, tenantID, headingName string) (string, error) {
	var headingID string
	err := tx.QueryRow(ctx, `
		SELECT id::text
		FROM contact_headings
		WHERE tenant_id = $1 AND archived_at IS NULL AND lower(name) = lower($2)
		LIMIT 1`,
		tenantID, headingName,
	).Scan(&headingID)
	if err == nil {
		return headingID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("contacts: match heading for copy: %w", err)
	}

	err = tx.QueryRow(ctx, `
		SELECT id::text
		FROM contact_headings
		WHERE tenant_id = $1 AND archived_at IS NULL AND lower(name) = 'otros'
		LIMIT 1`,
		tenantID,
	).Scan(&headingID)
	if err == nil {
		return headingID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("contacts: lookup otros heading: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO contact_headings (tenant_id, name)
		VALUES ($1, 'otros')
		ON CONFLICT (tenant_id, lower(name)) DO NOTHING`,
		tenantID,
	); err != nil {
		return "", fmt.Errorf("contacts: ensure otros heading: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT id::text
		FROM contact_headings
		WHERE tenant_id = $1 AND archived_at IS NULL AND lower(name) = 'otros'
		LIMIT 1`,
		tenantID,
	).Scan(&headingID); err != nil {
		return "", fmt.Errorf("contacts: load ensured otros heading: %w", err)
	}
	return headingID, nil
}

func applyApprovedShareSnapshots(ctx context.Context, tx pgx.Tx, tenantID string, ids []string, byID map[string]int, contacts []Contact) error {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ON (l.contact_id)
		       l.contact_id::text, sc.heading_name, sc.name, sc.phone, sc.email, sc.zone, sc.reference_prices
		FROM shared_contact_links l
		JOIN shared_contacts sc ON sc.id = l.shared_contact_id
		WHERE l.tenant_id = $1 AND l.contact_id = ANY($2::uuid[]) AND sc.status = 'approved'
		ORDER BY l.contact_id, l.seq DESC`,
		tenantID, ids,
	)
	if err != nil {
		return fmt.Errorf("contacts: load approved share snapshots: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var contactID string
		var snap SharedContact
		var pricesRaw []byte
		if err := rows.Scan(&contactID, &snap.HeadingName, &snap.Name, &snap.Phone, &snap.Email, &snap.Zone, &pricesRaw); err != nil {
			return err
		}
		if len(pricesRaw) > 0 {
			if err := json.Unmarshal(pricesRaw, &snap.ReferencePrices); err != nil {
				return fmt.Errorf("contacts: unmarshal approved share prices: %w", err)
			}
		}
		if idx, ok := byID[contactID]; ok {
			contacts[idx].HasApprovedShare = true
			contacts[idx].ShareOutdated = !matchesApprovedSnapshot(contacts[idx], snap)
		}
	}
	return rows.Err()
}

func matchesApprovedSnapshot(c Contact, snap SharedContact) bool {
	if c.HeadingName != snap.HeadingName ||
		c.Name != snap.Name ||
		c.Phone != snap.Phone ||
		c.Email != snap.Email ||
		c.Zone != snap.Zone {
		return false
	}
	if len(c.ReferencePrices) != len(snap.ReferencePrices) {
		return false
	}
	for i, rp := range c.ReferencePrices {
		sp := snap.ReferencePrices[i]
		if rp.Description != sp.Description || rp.AmountCents != sp.AmountCents || rp.Date != sp.Date {
			return false
		}
	}
	return true
}

func foldAccentsLower(s string) string {
	return accentReplacer.Replace(strings.ToLower(s))
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
