package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/contacts"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

func (s *server) GetContactHeadings(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	rows, err := s.contacts.ListHeadings(r.Context(), sess)
	if err != nil {
		writeContactsError(w, "list contact headings", err)
		return
	}
	out := make([]oapi.ContactHeading, len(rows))
	for i, h := range rows {
		out[i] = toHeadingDTO(h)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) PostContactHeading(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.contacts.AuthorizeWrite(r.Context(), sess); err != nil {
		writeContactsError(w, "create contact heading", err)
		return
	}
	var req oapi.PostContactHeadingJSONRequestBody
	if !decodeStrict(w, r, &req) {
		return
	}
	h, err := s.contacts.CreateHeading(r.Context(), sess, req.Name)
	if err != nil {
		writeContactsError(w, "create contact heading", err)
		return
	}
	writeJSON(w, http.StatusCreated, toHeadingDTO(h))
}

func (s *server) PatchContactHeading(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.contacts.AuthorizeWrite(r.Context(), sess); err != nil {
		writeContactsError(w, "rename contact heading", err)
		return
	}
	var req oapi.PatchContactHeadingJSONRequestBody
	if !decodeStrict(w, r, &req) {
		return
	}
	h, err := s.contacts.RenameHeading(r.Context(), sess, id, req.Name)
	if err != nil {
		writeContactsError(w, "rename contact heading", err)
		return
	}
	writeJSON(w, http.StatusOK, toHeadingDTO(h))
}

func (s *server) PostContactHeadingArchive(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	h, err := s.contacts.ArchiveHeading(r.Context(), sess, id)
	if err != nil {
		writeContactsError(w, "archive contact heading", err)
		return
	}
	writeJSON(w, http.StatusOK, toHeadingDTO(h))
}

func (s *server) DeleteContactHeading(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.contacts.DeleteHeading(r.Context(), sess, id); err != nil {
		writeContactsError(w, "delete contact heading", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) GetContacts(w http.ResponseWriter, r *http.Request, params oapi.GetContactsParams) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	rows, err := s.contacts.ListContacts(r.Context(), sess, derefString(params.HeadingId), derefString(params.Q))
	if err != nil {
		writeContactsError(w, "list contacts", err)
		return
	}
	out := make([]oapi.Contact, len(rows))
	for i, c := range rows {
		out[i] = toContactDTO(c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) PostContact(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.contacts.AuthorizeWrite(r.Context(), sess); err != nil {
		writeContactsError(w, "create contact", err)
		return
	}
	var req oapi.PostContactJSONRequestBody
	if !decodeStrict(w, r, &req) {
		return
	}
	c, err := s.contacts.CreateContact(r.Context(), sess, contacts.CreateContactInput{
		Name:      req.Name,
		HeadingID: req.HeadingId,
		Phone:     derefString(req.Phone),
		Email:     derefString(req.Email),
		Zone:      derefString(req.Zone),
		Notes:     derefString(req.Notes),
	})
	if err != nil {
		writeContactsError(w, "create contact", err)
		return
	}
	writeJSON(w, http.StatusCreated, toContactDTO(c))
}

func (s *server) PatchContact(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.contacts.AuthorizeWrite(r.Context(), sess); err != nil {
		writeContactsError(w, "update contact", err)
		return
	}
	var req oapi.PatchContactJSONRequestBody
	if !decodeStrict(w, r, &req) {
		return
	}
	c, err := s.contacts.UpdateContact(r.Context(), sess, id, contacts.UpdateContactInput{
		Name:      req.Name,
		HeadingID: req.HeadingId,
		Phone:     req.Phone,
		Email:     req.Email,
		Zone:      req.Zone,
		Notes:     req.Notes,
	})
	if err != nil {
		writeContactsError(w, "update contact", err)
		return
	}
	writeJSON(w, http.StatusOK, toContactDTO(c))
}

func (s *server) DeleteContact(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.contacts.DeleteContact(r.Context(), sess, id); err != nil {
		writeContactsError(w, "delete contact", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) PostContactReferencePrice(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.contacts.AuthorizeWrite(r.Context(), sess); err != nil {
		writeContactsError(w, "add reference price", err)
		return
	}
	var req oapi.PostContactReferencePriceJSONRequestBody
	if !decodeStrict(w, r, &req) {
		return
	}
	rp, err := s.contacts.AddReferencePrice(r.Context(), sess, id, contacts.CreateReferencePriceInput{
		Description: req.Description,
		AmountCents: req.AmountCents,
		Date:        req.Date,
	})
	if err != nil {
		writeContactsError(w, "add reference price", err)
		return
	}
	writeJSON(w, http.StatusCreated, toReferencePriceDTO(rp))
}

func (s *server) DeleteContactReferencePrice(w http.ResponseWriter, r *http.Request, id, priceID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.contacts.DeleteReferencePrice(r.Context(), sess, id, priceID); err != nil {
		writeContactsError(w, "delete reference price", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeContactsError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, contacts.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, auth.ErrMFARequired):
		writeError(w, http.StatusForbidden, "mfa_required")
	case errors.Is(err, contacts.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, contacts.ErrHeadingExists):
		writeError(w, http.StatusConflict, "heading_exists")
	case errors.Is(err, contacts.ErrHeadingInUse):
		writeError(w, http.StatusConflict, "heading_in_use")
	case errors.Is(err, contacts.ErrReservedHeading):
		writeError(w, http.StatusConflict, "reserved_heading")
	case errors.Is(err, contacts.ErrNameRequired):
		writeError(w, http.StatusUnprocessableEntity, "name_required")
	case errors.Is(err, contacts.ErrInvalidHeading):
		writeError(w, http.StatusUnprocessableEntity, "invalid_heading")
	case errors.Is(err, contacts.ErrInvalidEmail):
		writeError(w, http.StatusUnprocessableEntity, "invalid_email")
	case errors.Is(err, contacts.ErrDescriptionRequired):
		writeError(w, http.StatusUnprocessableEntity, "description_required")
	case errors.Is(err, contacts.ErrInvalidAmount):
		writeError(w, http.StatusUnprocessableEntity, "invalid_amount")
	case errors.Is(err, contacts.ErrFutureDate):
		writeError(w, http.StatusUnprocessableEntity, "future_date")
	case errors.Is(err, contacts.ErrInvalidDate):
		writeError(w, http.StatusUnprocessableEntity, "invalid_date")
	default:
		slog.Error(op, "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

func toHeadingDTO(h contacts.Heading) oapi.ContactHeading {
	return oapi.ContactHeading{
		Id:       h.ID,
		Name:     h.Name,
		Archived: h.Archived,
		Reserved: h.Reserved,
	}
}

func toReferencePriceDTO(rp contacts.ReferencePrice) oapi.ReferencePrice {
	return oapi.ReferencePrice{
		Id:          rp.ID,
		Description: rp.Description,
		AmountCents: rp.AmountCents,
		Date:        rp.Date,
		CreatedAt:   rp.CreatedAt,
	}
}

func toContactDTO(c contacts.Contact) oapi.Contact {
	prices := make([]oapi.ReferencePrice, len(c.ReferencePrices))
	for i, rp := range c.ReferencePrices {
		prices[i] = toReferencePriceDTO(rp)
	}
	return oapi.Contact{
		Id:              c.ID,
		HeadingId:       c.HeadingID,
		HeadingName:     c.HeadingName,
		HeadingArchived: c.HeadingArchived,
		Name:            c.Name,
		Phone:           c.Phone,
		Email:           c.Email,
		Zone:            c.Zone,
		Notes:           c.Notes,
		ReferencePrices: prices,
		CreatedAt:       c.CreatedAt,
		UpdatedAt:       c.UpdatedAt,
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
