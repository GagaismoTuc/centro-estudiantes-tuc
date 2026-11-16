package elections

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

const (
	// MaxListNameLen is the longest election list name, in characters.
	MaxListNameLen = 80
	// MaxReasonLen is the longest rejection reason, in characters.
	MaxReasonLen = 500
)

var (
	// ErrSubmissionClosed means the election is not in its submission period.
	ErrSubmissionClosed = errors.New("elections: submission closed")
	// ErrListFrozen means an approved list cannot be edited, approved again, or rejected.
	ErrListFrozen = errors.New("elections: list frozen")
	// ErrListRejected means a rejected list cannot be edited, approved, or rejected again.
	ErrListRejected = errors.New("elections: list rejected")
	// ErrListNameTaken means another non-rejected list in the election already has that name.
	ErrListNameTaken = errors.New("elections: list name taken")
	// ErrCandidateOnOtherList means a candidate already sits on a pending or approved list in the election.
	ErrCandidateOnOtherList = errors.New("elections: candidate on other list")
	// ErrInvalidCandidates means positions do not match the election or a student appears twice.
	ErrInvalidCandidates = errors.New("elections: invalid candidates")
	// ErrCandidateInCommission means a candidate is a member of the election's Electoral commission.
	ErrCandidateInCommission = errors.New("elections: candidate in commission")
	// ErrReasonRequired means the rejection reason is blank or longer than MaxReasonLen characters.
	ErrReasonRequired = errors.New("elections: reason required")
	// ErrInvalidRepresentative means the representative roster member id is missing or empty.
	ErrInvalidRepresentative = errors.New("elections: invalid representative")
)

// ListStatus is a list's approval state.
type ListStatus string

const (
	ListPending  ListStatus = "pending"
	ListApproved ListStatus = "approved"
	ListRejected ListStatus = "rejected"
)

// CandidateInput names one candidate for one position in a list submission or edit.
type CandidateInput struct {
	Position       string
	RosterMemberID string
}

// ListInput holds the editable fields of an election list.
type ListInput struct {
	Name       string
	Candidates []CandidateInput
}

// ListCandidate is a stored candidate on an election list.
type ListCandidate struct {
	Position       string
	RosterMemberID string
	FullName       string
}

// ElectionList is a stored election list with its representative and candidates ordered by position.
type ElectionList struct {
	ID                           string
	ElectionID                   string
	Name                         string
	Status                       ListStatus
	RejectionReason              *string
	RepresentativeRosterMemberID string
	RepresentativeFullName       string
	Candidates                   []ListCandidate
	ReviewedAt                   *time.Time
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
}

// ListsView holds the lists visible to the caller and the caller's own roster member id, if any.
type ListsView struct {
	RosterMemberID *string
	Lists          []ElectionList
}

type resolvedCandidate struct {
	order          int
	rosterMemberID string
}

// asRosterMember runs fn when sess belongs to a member of the tenant who is linked to a roster
// entry (ErrForbidden otherwise). It does not require a second factor beyond the session.
func (s *Store) asRosterMember(ctx context.Context, sess auth.Session, fn func(pgx.Tx, access) error) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		a, err := readAccess(ctx, tx, sess.TenantID, sess.UserID)
		if err != nil {
			return err
		}
		if !a.member || a.rosterMemberID == "" {
			return ErrForbidden
		}
		return fn(tx, a)
	})
}

// AuthorizeSubmitList checks that sess belongs to a school member linked to a roster entry,
// before reading the request body.
func (s *Store) AuthorizeSubmitList(ctx context.Context, sess auth.Session) error {
	return s.asRosterMember(ctx, sess, func(pgx.Tx, access) error { return nil })
}

// SubmitList presents a new list on behalf of the session user's roster entry.
func (s *Store) SubmitList(ctx context.Context, sess auth.Session, electionID string, in ListInput) (ElectionList, error) {
	var out ElectionList
	err := s.asRosterMember(ctx, sess, func(tx pgx.Tx, a access) error {
		var err error
		out, err = s.createList(ctx, tx, sess.TenantID, electionID, a.rosterMemberID, in)
		return err
	})
	return out, err
}

// CommissionSubmitList loads a new list as a commission member on behalf of representativeID.
func (s *Store) CommissionSubmitList(ctx context.Context, sess auth.Session, electionID, representativeID string, in ListInput) (ElectionList, error) {
	var out ElectionList
	err := s.asCommissionMember(ctx, sess, func(tx pgx.Tx) error {
		rep := strings.ToLower(strings.TrimSpace(representativeID))
		if rep == "" {
			return ErrInvalidRepresentative
		}
		if !uuidPattern.MatchString(rep) {
			return ErrNotFound
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM roster_members WHERE tenant_id = $1 AND id = $2)`,
			sess.TenantID, rep).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		var err error
		out, err = s.createList(ctx, tx, sess.TenantID, electionID, rep, in)
		return err
	})
	return out, err
}

// AuthorizeEditList verifies before reading the request body that sess may edit listID (either as
// its representative or as a commission member with a verified second factor). Hidden non-approved
// lists return ErrNotFound to unrelated members.
func (s *Store) AuthorizeEditList(ctx context.Context, sess auth.Session, electionID, listID string) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		_, err := s.authorizeListEditor(ctx, tx, sess, electionID, listID, false)
		return err
	})
}

func (s *Store) authorizeListEditor(ctx context.Context, tx pgx.Tx, sess auth.Session, electionID, listID string, lock bool) (access, error) {
	a, err := readAccess(ctx, tx, sess.TenantID, sess.UserID)
	if err != nil {
		return a, err
	}
	if !a.member {
		return a, ErrForbidden
	}
	if !uuidPattern.MatchString(electionID) || !uuidPattern.MatchString(listID) {
		return a, ErrNotFound
	}
	var repID, status string
	err = tx.QueryRow(ctx, `
		SELECT representative_id::text, status FROM election_lists
		WHERE tenant_id = $1 AND election_id = $2 AND id = $3`,
		sess.TenantID, electionID, listID).Scan(&repID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	isRep := a.rosterMemberID != "" && strings.EqualFold(a.rosterMemberID, repID)
	if isRep {
		return a, nil
	}
	if !a.onCommission {
		if status != string(ListApproved) {
			return a, ErrNotFound
		}
		return a, ErrForbidden
	}
	if lock {
		if err := lockCommission(ctx, tx, sess.TenantID); err != nil {
			return a, err
		}
		a, err = readAccess(ctx, tx, sess.TenantID, sess.UserID)
		if err != nil {
			return a, err
		}
		if !a.onCommission {
			if status != string(ListApproved) {
				return a, ErrNotFound
			}
			return a, ErrForbidden
		}
	}
	if err := auth.RequireMFA(sess); err != nil {
		return a, err
	}
	return a, nil
}

// EditList updates a pending list's name and candidates when called by its representative or a
// commission member during the submission period.
func (s *Store) EditList(ctx context.Context, sess auth.Session, electionID, listID string, in ListInput) (ElectionList, error) {
	var out ElectionList
	if sess.TenantID == "" {
		return out, ErrForbidden
	}
	err := db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		if _, err := s.authorizeListEditor(ctx, tx, sess, electionID, listID, true); err != nil {
			return err
		}
		e, err := s.getElectionForSubmission(ctx, tx, sess.TenantID, electionID)
		if err != nil {
			return err
		}
		cur, err := lockList(ctx, tx, sess.TenantID, electionID, listID)
		if err != nil {
			return err
		}
		if err := ensurePending(cur.Status); err != nil {
			return err
		}
		name, cands, err := validateListInput(ctx, tx, sess.TenantID, e, in)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE election_lists SET name = $4, updated_at = $5
			WHERE tenant_id = $1 AND election_id = $2 AND id = $3`,
			sess.TenantID, electionID, listID, name, s.stamp()); err != nil {
			return mapListWriteErr(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM election_list_candidates WHERE tenant_id = $1 AND list_id = $2`,
			sess.TenantID, listID); err != nil {
			return err
		}
		if err := insertListCandidates(ctx, tx, sess.TenantID, electionID, listID, cands); err != nil {
			return err
		}
		out, err = getList(ctx, tx, sess.TenantID, electionID, listID)
		return err
	})
	return out, err
}

// ApproveList marks a pending list as approved during the submission period.
func (s *Store) ApproveList(ctx context.Context, sess auth.Session, electionID, listID string) (ElectionList, error) {
	var out ElectionList
	err := s.asCommissionMember(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(electionID) || !uuidPattern.MatchString(listID) {
			return ErrNotFound
		}
		if _, err := s.getElectionForSubmission(ctx, tx, sess.TenantID, electionID); err != nil {
			return err
		}
		cur, err := lockList(ctx, tx, sess.TenantID, electionID, listID)
		if err != nil {
			return err
		}
		if err := ensurePending(cur.Status); err != nil {
			return err
		}
		now := s.stamp()
		if _, err := tx.Exec(ctx, `
			UPDATE election_lists SET status = 'approved', reviewed_at = $4, updated_at = $4
			WHERE tenant_id = $1 AND election_id = $2 AND id = $3`,
			sess.TenantID, electionID, listID, now); err != nil {
			return err
		}
		out, err = getList(ctx, tx, sess.TenantID, electionID, listID)
		return err
	})
	return out, err
}

// RejectList marks a pending list as rejected with reason, freeing its candidates and name.
func (s *Store) RejectList(ctx context.Context, sess auth.Session, electionID, listID, reason string) (ElectionList, error) {
	var out ElectionList
	err := s.asCommissionMember(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(electionID) || !uuidPattern.MatchString(listID) {
			return ErrNotFound
		}
		if _, err := s.getElectionForSubmission(ctx, tx, sess.TenantID, electionID); err != nil {
			return err
		}
		cur, err := lockList(ctx, tx, sess.TenantID, electionID, listID)
		if err != nil {
			return err
		}
		if err := ensurePending(cur.Status); err != nil {
			return err
		}
		trimmed := strings.TrimSpace(reason)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > MaxReasonLen {
			return ErrReasonRequired
		}
		now := s.stamp()
		// Updating is_active=false cascades to election_list_candidates via the composite FK.
		if _, err := tx.Exec(ctx, `
			UPDATE election_lists SET status = 'rejected', is_active = false, rejection_reason = $4, reviewed_at = $5, updated_at = $5
			WHERE tenant_id = $1 AND election_id = $2 AND id = $3`,
			sess.TenantID, electionID, listID, trimmed, now); err != nil {
			return err
		}
		out, err = getList(ctx, tx, sess.TenantID, electionID, listID)
		return err
	})
	return out, err
}

// ListLists returns the lists of electionID visible to sess (approved lists for everyone; pending
// and rejected lists only to the election's commission or each list's representative).
func (s *Store) ListLists(ctx context.Context, sess auth.Session, electionID string) (ListsView, error) {
	var out ListsView
	if sess.TenantID == "" {
		return out, ErrForbidden
	}
	err := db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		a, err := readAccess(ctx, tx, sess.TenantID, sess.UserID)
		if err != nil {
			return err
		}
		if !a.member {
			return ErrForbidden
		}
		if !uuidPattern.MatchString(electionID) {
			return ErrNotFound
		}
		e, err := getElection(ctx, tx, `e.id = $2`, sess.TenantID, electionID)
		if err != nil {
			return err
		}
		if a.rosterMemberID != "" {
			id := a.rosterMemberID
			out.RosterMemberID = &id
		}
		isComm, err := isOnElectionCommission(ctx, tx, sess.TenantID, e.CommissionID, a.rosterMemberID)
		if err != nil {
			return err
		}
		out.Lists, err = queryLists(ctx, tx, sess.TenantID, electionID, a.rosterMemberID, isComm)
		return err
	})
	return out, err
}

// GetList returns one list of electionID if visible to sess, or ErrNotFound.
func (s *Store) GetList(ctx context.Context, sess auth.Session, electionID, listID string) (ElectionList, error) {
	var out ElectionList
	if sess.TenantID == "" {
		return out, ErrForbidden
	}
	err := db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		a, err := readAccess(ctx, tx, sess.TenantID, sess.UserID)
		if err != nil {
			return err
		}
		if !a.member {
			return ErrForbidden
		}
		if !uuidPattern.MatchString(electionID) || !uuidPattern.MatchString(listID) {
			return ErrNotFound
		}
		e, err := getElection(ctx, tx, `e.id = $2`, sess.TenantID, electionID)
		if err != nil {
			return err
		}
		l, err := getList(ctx, tx, sess.TenantID, electionID, listID)
		if err != nil {
			return err
		}
		if l.Status != ListApproved {
			isComm, err := isOnElectionCommission(ctx, tx, sess.TenantID, e.CommissionID, a.rosterMemberID)
			if err != nil {
				return err
			}
			isRep := a.rosterMemberID != "" && strings.EqualFold(a.rosterMemberID, l.RepresentativeRosterMemberID)
			if !isComm && !isRep {
				return ErrNotFound
			}
		}
		out = l
		return nil
	})
	return out, err
}

// ListRosterCandidates returns all roster members of the tenant (names and ids only) for any
// school member, after verifying electionID exists in the tenant.
func (s *Store) ListRosterCandidates(ctx context.Context, sess auth.Session, electionID string) ([]CommissionMember, error) {
	out := []CommissionMember{}
	err := s.asMember(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(electionID) {
			return ErrNotFound
		}
		if _, err := getElection(ctx, tx, `e.id = $2`, sess.TenantID, electionID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id::text, full_name FROM roster_members
			WHERE tenant_id = $1 ORDER BY full_name, id`, sess.TenantID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CommissionMember])
		return err
	})
	return out, err
}

func (s *Store) createList(ctx context.Context, tx pgx.Tx, tenant, electionID, repID string, in ListInput) (ElectionList, error) {
	if !uuidPattern.MatchString(electionID) {
		return ElectionList{}, ErrNotFound
	}
	e, err := s.getElectionForSubmission(ctx, tx, tenant, electionID)
	if err != nil {
		return ElectionList{}, err
	}
	name, cands, err := validateListInput(ctx, tx, tenant, e, in)
	if err != nil {
		return ElectionList{}, err
	}
	now := s.stamp()
	var listID string
	err = tx.QueryRow(ctx, `
		INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'pending', $5, $5) RETURNING id::text`,
		tenant, electionID, name, repID, now).Scan(&listID)
	if err != nil {
		return ElectionList{}, mapListWriteErr(err)
	}
	if err := insertListCandidates(ctx, tx, tenant, electionID, listID, cands); err != nil {
		return ElectionList{}, err
	}
	return getList(ctx, tx, tenant, electionID, listID)
}

func (s *Store) getElectionForSubmission(ctx context.Context, tx pgx.Tx, tenant, electionID string) (Election, error) {
	if !uuidPattern.MatchString(electionID) {
		return Election{}, ErrNotFound
	}
	e, err := getElection(ctx, tx, `e.id = $2 FOR SHARE OF e`, tenant, electionID)
	if err != nil {
		return Election{}, err
	}
	if e.PublishedAt != nil || e.AnnulledAt != nil || State(s.now(), e) != StatusSubmission {
		return Election{}, ErrSubmissionClosed
	}
	return e, nil
}

func validateListInput(ctx context.Context, tx pgx.Tx, tenant string, e Election, in ListInput) (string, []resolvedCandidate, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > MaxListNameLen {
		return "", nil, ErrInvalidName
	}
	if len(in.Candidates) != len(e.Positions) {
		return "", nil, ErrInvalidCandidates
	}
	posOrder := make(map[string]int, len(e.Positions))
	for i, p := range e.Positions {
		posOrder[p] = i + 1
	}
	seenOrder := make(map[int]bool, len(e.Positions))
	seenCand := make(map[string]bool, len(e.Positions))
	resolved := make([]resolvedCandidate, 0, len(in.Candidates))
	ids := make([]string, 0, len(in.Candidates))
	for _, c := range in.Candidates {
		pos := strings.TrimSpace(c.Position)
		order, ok := posOrder[pos]
		if !ok || seenOrder[order] {
			return "", nil, ErrInvalidCandidates
		}
		seenOrder[order] = true
		cid := strings.ToLower(strings.TrimSpace(c.RosterMemberID))
		if cid == "" || seenCand[cid] {
			return "", nil, ErrInvalidCandidates
		}
		seenCand[cid] = true
		resolved = append(resolved, resolvedCandidate{order: order, rosterMemberID: cid})
		ids = append(ids, cid)
	}
	for _, id := range ids {
		if !uuidPattern.MatchString(id) {
			return "", nil, ErrNotFound
		}
	}
	var found int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM roster_members WHERE tenant_id = $1 AND id = ANY($2::uuid[])`,
		tenant, ids).Scan(&found); err != nil {
		return "", nil, err
	}
	if found != len(ids) {
		return "", nil, ErrNotFound
	}
	var inCommission bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT FROM electoral_commission_members
		               WHERE tenant_id = $1 AND commission_id = $2 AND roster_member_id = ANY($3::uuid[]))`,
		tenant, e.CommissionID, ids).Scan(&inCommission); err != nil {
		return "", nil, err
	}
	if inCommission {
		return "", nil, ErrCandidateInCommission
	}
	slices.SortFunc(resolved, func(a, b resolvedCandidate) int { return a.order - b.order })
	return name, resolved, nil
}

func insertListCandidates(ctx context.Context, tx pgx.Tx, tenant, electionID, listID string, cands []resolvedCandidate) error {
	orders := make([]int, len(cands))
	ids := make([]string, len(cands))
	for i, c := range cands {
		orders[i], ids[i] = c.order, c.rosterMemberID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO election_list_candidates (tenant_id, election_id, list_id, position_order, candidate_id)
		SELECT $1, $2, $3, ord, cid FROM unnest($4::int[], $5::uuid[]) AS t(ord, cid)`,
		tenant, electionID, listID, orders, ids)
	return mapListWriteErr(err)
}

func mapListWriteErr(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return err
	}
	switch pgErr.ConstraintName {
	case "election_lists_name_active_key":
		return ErrListNameTaken
	case "election_list_candidates_active_candidate_key":
		return ErrCandidateOnOtherList
	default:
		return err
	}
}

type lockedList struct {
	Status ListStatus
}

func lockList(ctx context.Context, tx pgx.Tx, tenant, electionID, listID string) (lockedList, error) {
	var out lockedList
	var status string
	err := tx.QueryRow(ctx, `
		SELECT status FROM election_lists l
		WHERE l.tenant_id = $1 AND l.election_id = $2 AND l.id = $3 FOR UPDATE OF l`,
		tenant, electionID, listID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	out.Status = ListStatus(status)
	return out, err
}

func ensurePending(status ListStatus) error {
	switch status {
	case ListApproved:
		return ErrListFrozen
	case ListRejected:
		return ErrListRejected
	default:
		return nil
	}
}

func isOnElectionCommission(ctx context.Context, tx pgx.Tx, tenant, commissionID, rosterMemberID string) (bool, error) {
	if rosterMemberID == "" {
		return false, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT FROM electoral_commission_members
		               WHERE tenant_id = $1 AND commission_id = $2 AND roster_member_id = $3)`,
		tenant, commissionID, rosterMemberID).Scan(&ok)
	return ok, err
}

func getList(ctx context.Context, tx pgx.Tx, tenant, electionID, listID string) (ElectionList, error) {
	var l ElectionList
	var status string
	err := tx.QueryRow(ctx, `
		SELECT l.id::text, l.election_id::text, l.name, l.status, l.rejection_reason,
		       l.representative_id::text, rep.full_name, l.reviewed_at, l.created_at, l.updated_at
		FROM election_lists l
		JOIN roster_members rep ON rep.tenant_id = l.tenant_id AND rep.id = l.representative_id
		WHERE l.tenant_id = $1 AND l.election_id = $2 AND l.id = $3`,
		tenant, electionID, listID).
		Scan(&l.ID, &l.ElectionID, &l.Name, &status, &l.RejectionReason,
			&l.RepresentativeRosterMemberID, &l.RepresentativeFullName, &l.ReviewedAt, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	l.Status = ListStatus(status)
	rows, err := tx.Query(ctx, `
		SELECT p.name, m.id::text, m.full_name
		FROM election_list_candidates c
		JOIN election_positions p ON p.tenant_id = c.tenant_id AND p.election_id = c.election_id AND p.position_order = c.position_order
		JOIN roster_members m ON m.tenant_id = c.tenant_id AND m.id = c.candidate_id
		WHERE c.tenant_id = $1 AND c.list_id = $2
		ORDER BY c.position_order`, tenant, listID)
	if err != nil {
		return l, err
	}
	l.Candidates, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ListCandidate])
	return l, err
}

func queryLists(ctx context.Context, tx pgx.Tx, tenant, electionID, rosterMemberID string, includeAll bool) ([]ElectionList, error) {
	var repParam *string
	if rosterMemberID != "" {
		repParam = &rosterMemberID
	}
	rows, err := tx.Query(ctx, `
		SELECT l.id::text, l.election_id::text, l.name, l.status, l.rejection_reason,
		       l.representative_id::text, rep.full_name, l.reviewed_at, l.created_at, l.updated_at
		FROM election_lists l
		JOIN roster_members rep ON rep.tenant_id = l.tenant_id AND rep.id = l.representative_id
		WHERE l.tenant_id = $1 AND l.election_id = $2
		  AND (l.status = 'approved' OR $3::boolean OR l.representative_id = $4::uuid)
		ORDER BY l.created_at, l.id`, tenant, electionID, includeAll, repParam)
	if err != nil {
		return nil, err
	}
	lists, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ElectionList, error) {
		var l ElectionList
		var status string
		err := r.Scan(&l.ID, &l.ElectionID, &l.Name, &status, &l.RejectionReason,
			&l.RepresentativeRosterMemberID, &l.RepresentativeFullName, &l.ReviewedAt, &l.CreatedAt, &l.UpdatedAt)
		l.Status = ListStatus(status)
		l.Candidates = []ListCandidate{}
		return l, err
	})
	if err != nil || len(lists) == 0 {
		return lists, err
	}
	listIDs := make([]string, len(lists))
	byID := make(map[string]int, len(lists))
	for i, l := range lists {
		listIDs[i] = l.ID
		byID[l.ID] = i
	}
	cRows, err := tx.Query(ctx, `
		SELECT c.list_id::text, p.name, m.id::text, m.full_name
		FROM election_list_candidates c
		JOIN election_positions p ON p.tenant_id = c.tenant_id AND p.election_id = c.election_id AND p.position_order = c.position_order
		JOIN roster_members m ON m.tenant_id = c.tenant_id AND m.id = c.candidate_id
		WHERE c.tenant_id = $1 AND c.list_id = ANY($2::uuid[])
		ORDER BY c.list_id, c.position_order`, tenant, listIDs)
	if err != nil {
		return nil, err
	}
	defer cRows.Close()
	for cRows.Next() {
		var lid string
		var cand ListCandidate
		if err := cRows.Scan(&lid, &cand.Position, &cand.RosterMemberID, &cand.FullName); err != nil {
			return nil, err
		}
		if idx, ok := byID[lid]; ok {
			lists[idx].Candidates = append(lists[idx].Candidates, cand)
		}
	}
	return lists, cRows.Err()
}
