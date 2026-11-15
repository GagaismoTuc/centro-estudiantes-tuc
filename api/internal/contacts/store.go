// Package contacts stores each school's contact headings, contacts, and dated reference prices.
package contacts

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

const (
	reservedHeadingName = "otros"
	maxEmailLen         = 254
)

// Tucumán (ART) is UTC-3 year-round; used to determine "today" when rejecting future price dates.
var tucumanZone = time.FixedZone("ART", -3*3600)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var (
	// ErrForbidden means the session user lacks the required role in the active tenant.
	ErrForbidden = errors.New("contacts: forbidden")
	// ErrNotFound means the heading, contact, or reference price does not exist in the active tenant.
	ErrNotFound = errors.New("contacts: not found")
	// ErrNameRequired means the heading or contact name is empty after trimming.
	ErrNameRequired = errors.New("contacts: name required")
	// ErrHeadingExists means another heading in the school already has that name (case-insensitive).
	ErrHeadingExists = errors.New("contacts: heading exists")
	// ErrHeadingInUse means the heading cannot be deleted because contacts (or budget lines) reference it.
	ErrHeadingInUse = errors.New("contacts: heading in use")
	// ErrReservedHeading means the reserved "otros" heading cannot be renamed, archived, or deleted.
	ErrReservedHeading = errors.New("contacts: reserved heading")
	// ErrInvalidHeading means the heading is missing or archived.
	ErrInvalidHeading = errors.New("contacts: invalid heading")
	// ErrInvalidEmail means a non-empty contact email is not a valid bare email address.
	ErrInvalidEmail = errors.New("contacts: invalid email")
	// ErrDescriptionRequired means the reference price description is empty after trimming.
	ErrDescriptionRequired = errors.New("contacts: description required")
	// ErrInvalidAmount means the reference price amount in cents is zero or negative.
	ErrInvalidAmount = errors.New("contacts: invalid amount")
	// ErrFutureDate means the reference price date is in the future.
	ErrFutureDate = errors.New("contacts: future date")
	// ErrInvalidDate means the reference price date is not a valid YYYY-MM-DD calendar date.
	ErrInvalidDate = errors.New("contacts: invalid date")
)

// Heading is a contact heading owned by a school.
type Heading struct {
	ID       string
	Name     string
	Archived bool
	Reserved bool
}

// ReferencePrice is a dated quote in integer cents for a contact.
type ReferencePrice struct {
	ID          string
	Description string
	AmountCents int64
	Date        string
	CreatedAt   time.Time
}

// Contact is a provider entry in a school's contact directory.
type Contact struct {
	ID              string
	HeadingID       string
	HeadingName     string
	HeadingArchived bool
	Name            string
	Phone           string
	Email           string
	Zone            string
	Notes           string
	ReferencePrices []ReferencePrice
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CreateContactInput holds fields for creating a contact.
type CreateContactInput struct {
	Name      string
	HeadingID string
	Phone     string
	Email     string
	Zone      string
	Notes     string
}

// UpdateContactInput holds optional patch fields for updating a contact; nil leaves a field unchanged.
type UpdateContactInput struct {
	Name      *string
	HeadingID *string
	Phone     *string
	Email     *string
	Zone      *string
	Notes     *string
}

// CreateReferencePriceInput holds fields for adding a reference price to a contact.
type CreateReferencePriceInput struct {
	Description string
	AmountCents int64
	Date        string
}

// Store runs contact directory operations through the application pool.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewStore returns a Store using pool and clock now. It panics when now is nil.
func NewStore(pool *pgxpool.Pool, now func() time.Time) *Store {
	if now == nil {
		panic("contacts: NewStore needs a clock")
	}
	return &Store{pool: pool, now: now}
}

// AuthorizeRead checks that sess may read the contact directory (officer, school_admin, or veedor
// with a verified second factor) without reading directory rows.
func (s *Store) AuthorizeRead(ctx context.Context, sess auth.Session) error {
	return s.asReader(ctx, sess, func(pgx.Tx) error { return nil })
}

// AuthorizeWrite checks that sess may modify the contact directory (officer with a verified second
// factor) without reading or writing directory rows.
func (s *Store) AuthorizeWrite(ctx context.Context, sess auth.Session) error {
	return s.asOfficer(ctx, sess, func(pgx.Tx) error { return nil })
}

func (s *Store) asReader(ctx context.Context, sess auth.Session, fn func(pgx.Tx) error) error {
	return s.withRole(ctx, sess, `
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
		)`, fn)
}

func (s *Store) asOfficer(ctx context.Context, sess auth.Session, fn func(pgx.Tx) error) error {
	return s.withRole(ctx, sess, `
		SELECT EXISTS (
			SELECT 1 FROM memberships m
			WHERE m.tenant_id = $1 AND m.user_id = $2
			  AND (
				  m.role = 'officer'
				  OR EXISTS (
					  SELECT 1 FROM officer_positions p
					  WHERE p.tenant_id = m.tenant_id AND p.roster_member_id = m.roster_member_id
				  )
			  )
		)`, fn)
}

func (s *Store) withRole(ctx context.Context, sess auth.Session, roleQuery string, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, roleQuery, sess.TenantID, sess.UserID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
		if err := auth.RequireMFA(sess); err != nil {
			return err
		}
		return fn(tx)
	})
}

// ListHeadings returns all headings for the session's school, active first then alphabetical with
// "otros" last among active headings.
func (s *Store) ListHeadings(ctx context.Context, sess auth.Session) ([]Heading, error) {
	out := []Heading{}
	err := s.asReader(ctx, sess, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id::text, name, archived_at IS NOT NULL
			FROM contact_headings
			WHERE tenant_id = $1
			ORDER BY (archived_at IS NOT NULL), (lower(name) = 'otros'), lower(name), id`, sess.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h Heading
			if err := rows.Scan(&h.ID, &h.Name, &h.Archived); err != nil {
				return err
			}
			h.Reserved = isReservedHeading(h.Name)
			out = append(out, h)
		}
		return rows.Err()
	})
	return out, err
}

// CreateHeading adds a new heading to the school's directory.
func (s *Store) CreateHeading(ctx context.Context, sess auth.Session, name string) (Heading, error) {
	var out Heading
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return ErrNameRequired
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO contact_headings (tenant_id, name)
			VALUES ($1, $2)
			RETURNING id::text, name, archived_at IS NOT NULL`,
			sess.TenantID, trimmed).Scan(&out.ID, &out.Name, &out.Archived)
		if isUniqueViolation(err) {
			return ErrHeadingExists
		}
		if err != nil {
			return fmt.Errorf("contacts: create heading: %w", err)
		}
		out.Reserved = isReservedHeading(out.Name)
		return nil
	})
	return out, err
}

// RenameHeading changes a heading's name; "otros" cannot be renamed.
func (s *Store) RenameHeading(ctx context.Context, sess auth.Session, id, name string) (Heading, error) {
	var out Heading
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return ErrNameRequired
		}
		var current string
		err := tx.QueryRow(ctx, `
			SELECT name FROM contact_headings
			WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
			sess.TenantID, id).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if isReservedHeading(current) {
			return ErrReservedHeading
		}
		err = tx.QueryRow(ctx, `
			UPDATE contact_headings SET name = $3
			WHERE tenant_id = $1 AND id = $2
			RETURNING id::text, name, archived_at IS NOT NULL`,
			sess.TenantID, id, trimmed).Scan(&out.ID, &out.Name, &out.Archived)
		if isUniqueViolation(err) {
			return ErrHeadingExists
		}
		if err != nil {
			return fmt.Errorf("contacts: rename heading: %w", err)
		}
		out.Reserved = isReservedHeading(out.Name)
		return nil
	})
	return out, err
}

// ArchiveHeading marks a heading archived (idempotently); "otros" cannot be archived.
func (s *Store) ArchiveHeading(ctx context.Context, sess auth.Session, id string) (Heading, error) {
	var out Heading
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		var current string
		err := tx.QueryRow(ctx, `
			SELECT name FROM contact_headings
			WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
			sess.TenantID, id).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if isReservedHeading(current) {
			return ErrReservedHeading
		}
		err = tx.QueryRow(ctx, `
			UPDATE contact_headings SET archived_at = COALESCE(archived_at, $3)
			WHERE tenant_id = $1 AND id = $2
			RETURNING id::text, name, archived_at IS NOT NULL`,
			sess.TenantID, id, s.now().UTC()).Scan(&out.ID, &out.Name, &out.Archived)
		if err != nil {
			return fmt.Errorf("contacts: archive heading: %w", err)
		}
		out.Reserved = isReservedHeading(out.Name)
		return nil
	})
	return out, err
}

// DeleteHeading deletes a heading when unused; "otros" returns ErrReservedHeading and a heading
// referenced by contacts returns ErrHeadingInUse.
func (s *Store) DeleteHeading(ctx context.Context, sess auth.Session, id string) error {
	return s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		var current string
		err := tx.QueryRow(ctx, `
			SELECT name FROM contact_headings
			WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
			sess.TenantID, id).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if isReservedHeading(current) {
			return ErrReservedHeading
		}
		_, err = tx.Exec(ctx, `DELETE FROM contact_headings WHERE tenant_id = $1 AND id = $2`, sess.TenantID, id)
		if isForeignKeyViolation(err) {
			return ErrHeadingInUse
		}
		if err != nil {
			return fmt.Errorf("contacts: delete heading: %w", err)
		}
		return nil
	})
}

// ListContacts returns contacts in the session's school, optionally filtered by headingID and
// case-insensitive substring match against name or zone.
func (s *Store) ListContacts(ctx context.Context, sess auth.Session, headingID, query string) ([]Contact, error) {
	out := []Contact{}
	err := s.asReader(ctx, sess, func(tx pgx.Tx) error {
		args := []any{sess.TenantID}
		where := []string{"c.tenant_id = $1"}

		if h := strings.TrimSpace(headingID); h != "" {
			if !uuidPattern.MatchString(h) {
				return nil
			}
			args = append(args, h)
			where = append(where, fmt.Sprintf("c.heading_id = $%d", len(args)))
		}
		if q := strings.TrimSpace(query); q != "" {
			args = append(args, "%"+escapeLike(q)+"%")
			p := fmt.Sprintf("$%d", len(args))
			where = append(where, fmt.Sprintf(`(c.name ILIKE %s ESCAPE '\' OR c.zone ILIKE %s ESCAPE '\')`, p, p))
		}

		sql := `
			SELECT c.id::text, c.heading_id::text, h.name, h.archived_at IS NOT NULL,
			       c.name, c.phone, c.email, c.zone, c.notes, c.created_at, c.updated_at
			FROM contacts c
			JOIN contact_headings h ON h.tenant_id = c.tenant_id AND h.id = c.heading_id
			WHERE ` + strings.Join(where, " AND ") + `
			ORDER BY lower(c.name), c.id`

		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		var ids []string
		byID := map[string]int{}
		for rows.Next() {
			var c Contact
			if err := rows.Scan(
				&c.ID, &c.HeadingID, &c.HeadingName, &c.HeadingArchived,
				&c.Name, &c.Phone, &c.Email, &c.Zone, &c.Notes, &c.CreatedAt, &c.UpdatedAt,
			); err != nil {
				return err
			}
			c.ReferencePrices = []ReferencePrice{}
			byID[c.ID] = len(out)
			ids = append(ids, c.ID)
			out = append(out, c)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}

		pRows, err := tx.Query(ctx, `
			SELECT contact_id::text, id::text, description, amount_cents, to_char(quoted_on, 'YYYY-MM-DD'), created_at
			FROM contact_reference_prices
			WHERE tenant_id = $1 AND contact_id = ANY($2::uuid[])
			ORDER BY quoted_on DESC, created_at DESC, id`,
			sess.TenantID, ids)
		if err != nil {
			return err
		}
		defer pRows.Close()
		for pRows.Next() {
			var contactID string
			var rp ReferencePrice
			if err := pRows.Scan(&contactID, &rp.ID, &rp.Description, &rp.AmountCents, &rp.Date, &rp.CreatedAt); err != nil {
				return err
			}
			if idx, ok := byID[contactID]; ok {
				out[idx].ReferencePrices = append(out[idx].ReferencePrices, rp)
			}
		}
		return pRows.Err()
	})
	return out, err
}

// CreateContact creates a new contact under an active heading of the school.
func (s *Store) CreateContact(ctx context.Context, sess auth.Session, in CreateContactInput) (Contact, error) {
	var out Contact
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return ErrNameRequired
		}
		email, err := validateOptionalEmail(in.Email)
		if err != nil {
			return err
		}
		headingName, headingArchived, err := requireActiveHeading(ctx, tx, sess.TenantID, in.HeadingID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		err = tx.QueryRow(ctx, `
			INSERT INTO contacts (tenant_id, heading_id, name, phone, email, zone, notes, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
			RETURNING id::text, heading_id::text, name, phone, email, zone, notes, created_at, updated_at`,
			sess.TenantID, strings.TrimSpace(in.HeadingID), name,
			strings.TrimSpace(in.Phone), email, strings.TrimSpace(in.Zone), strings.TrimSpace(in.Notes), now,
		).Scan(&out.ID, &out.HeadingID, &out.Name, &out.Phone, &out.Email, &out.Zone, &out.Notes, &out.CreatedAt, &out.UpdatedAt)
		if err != nil {
			return fmt.Errorf("contacts: create contact: %w", err)
		}
		out.HeadingName = headingName
		out.HeadingArchived = headingArchived
		out.ReferencePrices = []ReferencePrice{}
		return nil
	})
	return out, err
}

// UpdateContact edits a contact's fields; changing headingId requires an active heading of the school.
func (s *Store) UpdateContact(ctx context.Context, sess auth.Session, id string, in UpdateContactInput) (Contact, error) {
	var out Contact
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		var curHeadingID, curName, curPhone, curEmail, curZone, curNotes string
		err := tx.QueryRow(ctx, `
			SELECT heading_id::text, name, phone, email, zone, notes
			FROM contacts
			WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
			sess.TenantID, id).Scan(&curHeadingID, &curName, &curPhone, &curEmail, &curZone, &curNotes)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		nextHeadingID := curHeadingID
		if in.HeadingID != nil {
			trimmed := strings.TrimSpace(*in.HeadingID)
			if trimmed != curHeadingID {
				if _, _, err := requireActiveHeading(ctx, tx, sess.TenantID, trimmed); err != nil {
					return err
				}
				nextHeadingID = trimmed
			}
		}

		nextName := curName
		if in.Name != nil {
			nextName = strings.TrimSpace(*in.Name)
			if nextName == "" {
				return ErrNameRequired
			}
		}

		nextEmail := curEmail
		if in.Email != nil {
			nextEmail, err = validateOptionalEmail(*in.Email)
			if err != nil {
				return err
			}
		}

		nextPhone := curPhone
		if in.Phone != nil {
			nextPhone = strings.TrimSpace(*in.Phone)
		}
		nextZone := curZone
		if in.Zone != nil {
			nextZone = strings.TrimSpace(*in.Zone)
		}
		nextNotes := curNotes
		if in.Notes != nil {
			nextNotes = strings.TrimSpace(*in.Notes)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE contacts
			SET heading_id = $3, name = $4, phone = $5, email = $6, zone = $7, notes = $8, updated_at = $9
			WHERE tenant_id = $1 AND id = $2`,
			sess.TenantID, id, nextHeadingID, nextName, nextPhone, nextEmail, nextZone, nextNotes, s.now().UTC()); err != nil {
			return fmt.Errorf("contacts: update contact: %w", err)
		}

		out, err = loadContact(ctx, tx, sess.TenantID, id)
		return err
	})
	return out, err
}

// DeleteContact removes a contact and its reference prices from the school's directory.
func (s *Store) DeleteContact(ctx context.Context, sess auth.Session, id string) error {
	return s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		tag, err := tx.Exec(ctx, `DELETE FROM contacts WHERE tenant_id = $1 AND id = $2`, sess.TenantID, id)
		if err != nil {
			return fmt.Errorf("contacts: delete contact: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// AddReferencePrice records a dated quote (in positive integer cents, not in the future) for a contact.
func (s *Store) AddReferencePrice(ctx context.Context, sess auth.Session, contactID string, in CreateReferencePriceInput) (ReferencePrice, error) {
	var out ReferencePrice
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(contactID) {
			return ErrNotFound
		}
		desc := strings.TrimSpace(in.Description)
		if desc == "" {
			return ErrDescriptionRequired
		}
		if in.AmountCents <= 0 {
			return ErrInvalidAmount
		}
		dateStr := strings.TrimSpace(in.Date)
		if _, err := time.Parse("2006-01-02", dateStr); err != nil {
			return ErrInvalidDate
		}
		today := s.now().In(tucumanZone).Format("2006-01-02")
		if dateStr > today {
			return ErrFutureDate
		}

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM contacts WHERE tenant_id = $1 AND id = $2)`,
			sess.TenantID, contactID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}

		err := tx.QueryRow(ctx, `
			INSERT INTO contact_reference_prices (tenant_id, contact_id, description, amount_cents, quoted_on, created_at)
			VALUES ($1, $2, $3, $4, $5::date, $6)
			RETURNING id::text, description, amount_cents, to_char(quoted_on, 'YYYY-MM-DD'), created_at`,
			sess.TenantID, contactID, desc, in.AmountCents, dateStr, s.now().UTC(),
		).Scan(&out.ID, &out.Description, &out.AmountCents, &out.Date, &out.CreatedAt)
		if err != nil {
			return fmt.Errorf("contacts: add reference price: %w", err)
		}
		return nil
	})
	return out, err
}

// DeleteReferencePrice removes a single reference price from a contact.
func (s *Store) DeleteReferencePrice(ctx context.Context, sess auth.Session, contactID, priceID string) error {
	return s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(contactID) || !uuidPattern.MatchString(priceID) {
			return ErrNotFound
		}
		tag, err := tx.Exec(ctx, `
			DELETE FROM contact_reference_prices
			WHERE tenant_id = $1 AND contact_id = $2 AND id = $3`,
			sess.TenantID, contactID, priceID)
		if err != nil {
			return fmt.Errorf("contacts: delete reference price: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func requireActiveHeading(ctx context.Context, tx pgx.Tx, tenantID, headingID string) (name string, archived bool, err error) {
	trimmed := strings.TrimSpace(headingID)
	if trimmed == "" {
		return "", false, ErrInvalidHeading
	}
	if !uuidPattern.MatchString(trimmed) {
		return "", false, ErrNotFound
	}
	err = tx.QueryRow(ctx, `
		SELECT name, archived_at IS NOT NULL
		FROM contact_headings
		WHERE tenant_id = $1 AND id = $2`,
		tenantID, trimmed).Scan(&name, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, err
	}
	if archived {
		return "", true, ErrInvalidHeading
	}
	return name, false, nil
}

func loadContact(ctx context.Context, tx pgx.Tx, tenantID, id string) (Contact, error) {
	var c Contact
	err := tx.QueryRow(ctx, `
		SELECT c.id::text, c.heading_id::text, h.name, h.archived_at IS NOT NULL,
		       c.name, c.phone, c.email, c.zone, c.notes, c.created_at, c.updated_at
		FROM contacts c
		JOIN contact_headings h ON h.tenant_id = c.tenant_id AND h.id = c.heading_id
		WHERE c.tenant_id = $1 AND c.id = $2`,
		tenantID, id,
	).Scan(
		&c.ID, &c.HeadingID, &c.HeadingName, &c.HeadingArchived,
		&c.Name, &c.Phone, &c.Email, &c.Zone, &c.Notes, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return Contact{}, err
	}
	c.ReferencePrices = []ReferencePrice{}
	rows, err := tx.Query(ctx, `
		SELECT id::text, description, amount_cents, to_char(quoted_on, 'YYYY-MM-DD'), created_at
		FROM contact_reference_prices
		WHERE tenant_id = $1 AND contact_id = $2
		ORDER BY quoted_on DESC, created_at DESC, id`,
		tenantID, id)
	if err != nil {
		return Contact{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var rp ReferencePrice
		if err := rows.Scan(&rp.ID, &rp.Description, &rp.AmountCents, &rp.Date, &rp.CreatedAt); err != nil {
			return Contact{}, err
		}
		c.ReferencePrices = append(c.ReferencePrices, rp)
	}
	return c, rows.Err()
}

func validateOptionalEmail(s string) (string, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", nil
	}
	a, err := mail.ParseAddress(trimmed)
	if err != nil || a.Name != "" || a.Address != trimmed || len(trimmed) > maxEmailLen {
		return "", ErrInvalidEmail
	}
	at := strings.IndexByte(trimmed, '@')
	if at < 0 {
		return "", ErrInvalidEmail
	}
	domain := trimmed[at+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrInvalidEmail
	}
	return trimmed, nil
}

func isReservedHeading(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), reservedHeadingName)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23001")
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
