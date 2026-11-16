package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
)

type headingDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
	Reserved bool   `json:"reserved"`
}

type referencePriceDTO struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	AmountCents int64  `json:"amountCents"`
	Date        string `json:"date"`
}

type contactDTO struct {
	ID              string              `json:"id"`
	HeadingID       string              `json:"headingId"`
	HeadingName     string              `json:"headingName"`
	HeadingArchived bool                `json:"headingArchived"`
	Name            string              `json:"name"`
	Phone           string              `json:"phone"`
	Email           string              `json:"email"`
	Zone            string              `json:"zone"`
	Notes           string              `json:"notes"`
	ReferencePrices []referencePriceDTO `json:"referencePrices"`
}

func seedHeadingsForTenant(t *testing.T, ctx context.Context, tenant string) map[string]string {
	t.Helper()
	if _, err := tdb.Super.Exec(ctx, `
		INSERT INTO contact_headings (tenant_id, name)
		SELECT $1, name FROM unnest($2::text[]) AS name
		ON CONFLICT (tenant_id, lower(name)) DO NOTHING`, tenant, auth.DefaultContactHeadings); err != nil {
		t.Fatal(err)
	}
	rows, err := tdb.Super.Query(ctx, `SELECT id::text, name FROM contact_headings WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		out[name] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func snapshotDirectory(t *testing.T, ctx context.Context, tenant string) (headings, contacts, prices int) {
	t.Helper()
	if err := tdb.Super.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM contact_headings WHERE tenant_id = $1),
		       (SELECT count(*) FROM contacts WHERE tenant_id = $1),
		       (SELECT count(*) FROM contact_reference_prices WHERE tenant_id = $1)`,
		tenant).Scan(&headings, &contacts, &prices); err != nil {
		t.Fatal(err)
	}
	return
}

func TestHeadingsCRUDValidationAndReserved(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.30"

	// Create heading.
	rec := f.do(http.MethodPost, "/api/contacts/headings", `{"name":"Fotografía"}`, ip, officer, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create heading = %d body=%s", rec.Code, rec.Body)
	}
	var created headingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "Fotografía" || created.Archived || created.Reserved {
		t.Fatalf("created heading = %+v", created)
	}

	hCount, cCount, pCount := snapshotDirectory(t, ctx, f.tenant)

	// Negative: empty name and case-insensitive duplicates leave DB untouched.
	wantResponse(t, f.do(http.MethodPost, "/api/contacts/headings", `{"name":"   "}`, ip, officer, nil), http.StatusUnprocessableEntity, `{"error":"name_required"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts/headings", `{"name":"GAZEBOS"}`, ip, officer, nil), http.StatusConflict, `{"error":"heading_exists"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts/headings", `{"name":"fotografía"}`, ip, officer, nil), http.StatusConflict, `{"error":"heading_exists"}`)
	wantResponse(t, f.do(http.MethodPatch, "/api/contacts/headings/"+created.ID, `{"name":""}`, ip, officer, nil), http.StatusUnprocessableEntity, `{"error":"name_required"}`)
	wantResponse(t, f.do(http.MethodPatch, "/api/contacts/headings/"+created.ID, `{"name":"Gazebos"}`, ip, officer, nil), http.StatusConflict, `{"error":"heading_exists"}`)

	// Reserved "otros" cannot be renamed, archived, or deleted.
	otrosID := headings["otros"]
	wantResponse(t, f.do(http.MethodPatch, "/api/contacts/headings/"+otrosID, `{"name":"Varios"}`, ip, officer, nil), http.StatusConflict, `{"error":"reserved_heading"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts/headings/"+otrosID+"/archive", "", ip, officer, nil), http.StatusConflict, `{"error":"reserved_heading"}`)
	wantResponse(t, f.do(http.MethodDelete, "/api/contacts/headings/"+otrosID, "", ip, officer, nil), http.StatusConflict, `{"error":"reserved_heading"}`)

	if h, c, p := snapshotDirectory(t, ctx, f.tenant); h != hCount || c != cCount || p != pCount {
		t.Fatalf("directory counts changed on rejected heading operations: %d/%d/%d vs %d/%d/%d", h, c, p, hCount, cCount, pCount)
	}

	// Rename created heading.
	rec = f.do(http.MethodPatch, "/api/contacts/headings/"+created.ID, `{"name":"Video y Foto"}`, ip, officer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename heading = %d body=%s", rec.Code, rec.Body)
	}

	// Delete heading with a contact -> 409 heading_in_use, then archive succeeds.
	rec = f.do(http.MethodPost, "/api/contacts", `{"name":"Foto Ficticia","headingId":"`+created.ID+`","phone":"381 555-0101"}`, ip, officer, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create contact = %d body=%s", rec.Code, rec.Body)
	}
	wantResponse(t, f.do(http.MethodDelete, "/api/contacts/headings/"+created.ID, "", ip, officer, nil), http.StatusConflict, `{"error":"heading_in_use"}`)

	rec = f.do(http.MethodPost, "/api/contacts/headings/"+created.ID+"/archive", "", ip, officer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive heading = %d body=%s", rec.Code, rec.Body)
	}
	var archived headingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &archived); err != nil || !archived.Archived {
		t.Fatalf("archived = %+v, err=%v", archived, err)
	}

	// Unused heading can be deleted.
	rec = f.do(http.MethodPost, "/api/contacts/headings", `{"name":"Temporal"}`, ip, officer, nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var temp headingDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &temp)
	wantResponse(t, f.do(http.MethodDelete, "/api/contacts/headings/"+temp.ID, "", ip, officer, nil), http.StatusNoContent, "")
	wantResponse(t, f.do(http.MethodDelete, "/api/contacts/headings/"+temp.ID, "", ip, officer, nil), http.StatusNotFound, `{"error":"not_found"}`)
}

func TestContactsReferencePricesEditDeleteAndSearch(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	otherTenant := newTenant(t, ctx)
	otherHeadings := seedHeadingsForTenant(t, ctx, otherTenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.31"

	// Example from R3: Gazebos Ficticios SRL.
	rec := f.do(http.MethodPost, "/api/contacts", `{
		"name": "Gazebos Ficticios SRL",
		"headingId": "`+headings["gazebos"]+`",
		"phone": "381 555-0000",
		"email": "ventas@gazebos-ficticios.example",
		"zone": "Yerba Buena",
		"notes": "Armado incluido"
	}`, ip, officer, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create contact = %d body=%s", rec.Code, rec.Body)
	}
	var contact contactDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &contact); err != nil {
		t.Fatal(err)
	}
	if contact.Name != "Gazebos Ficticios SRL" || contact.HeadingName != "gazebos" || contact.Zone != "Yerba Buena" {
		t.Fatalf("created contact = %+v", contact)
	}

	// Archive "seguridad" to test archived heading rejection.
	if r := f.do(http.MethodPost, "/api/contacts/headings/"+headings["seguridad"]+"/archive", "", ip, officer, nil); r.Code != http.StatusOK {
		t.Fatalf("archive seguridad = %d body=%s", r.Code, r.Body)
	}

	hCount, cCount, pCount := snapshotDirectory(t, ctx, f.tenant)

	// Negative R3 cases (no state changes).
	wantResponse(t, f.do(http.MethodPost, "/api/contacts", `{"name":"  ","headingId":"`+headings["gazebos"]+`"}`, ip, officer, nil), http.StatusUnprocessableEntity, `{"error":"name_required"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts", `{"name":"Otro","headingId":"`+headings["gazebos"]+`","email":"mal-email"}`, ip, officer, nil), http.StatusUnprocessableEntity, `{"error":"invalid_email"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts", `{"name":"Otro","headingId":"`+otherHeadings["gazebos"]+`"}`, ip, officer, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts", `{"name":"Otro","headingId":"`+headings["seguridad"]+`"}`, ip, officer, nil), http.StatusUnprocessableEntity, `{"error":"invalid_heading"}`)

	// Example from R4: Gazebo 3×3, $25.000 (2500000 cents), 2026-09-01 (fakeClock is 2026-11-13).
	rec = f.do(http.MethodPost, "/api/contacts/"+contact.ID+"/reference-prices", `{
		"description": "Gazebo 3×3",
		"amountCents": 2500000,
		"date": "2026-09-01"
	}`, ip, officer, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add price = %d body=%s", rec.Code, rec.Body)
	}
	var price referencePriceDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &price); err != nil {
		t.Fatal(err)
	}
	if price.AmountCents != 2500000 || price.Date != "2026-09-01" || price.Description != "Gazebo 3×3" {
		t.Fatalf("price = %+v", price)
	}

	// Negative R4 cases.
	_, cCount2, pCount2 := snapshotDirectory(t, ctx, f.tenant)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts/"+contact.ID+"/reference-prices", `{"description":"X","amountCents":0,"date":"2026-09-01"}`, ip, officer, nil), http.StatusUnprocessableEntity, `{"error":"invalid_amount"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts/"+contact.ID+"/reference-prices", `{"description":"X","amountCents":-500,"date":"2026-09-01"}`, ip, officer, nil), http.StatusUnprocessableEntity, `{"error":"invalid_amount"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts/"+contact.ID+"/reference-prices", `{"description":"X","amountCents":1000,"date":"2026-11-14"}`, ip, officer, nil), http.StatusUnprocessableEntity, `{"error":"future_date"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts/"+contact.ID+"/reference-prices", `{"description":"  ","amountCents":1000,"date":"2026-09-01"}`, ip, officer, nil), http.StatusUnprocessableEntity, `{"error":"description_required"}`)

	if h, c, p := snapshotDirectory(t, ctx, f.tenant); h != hCount || c != cCount2 || p != pCount2 {
		t.Fatalf("counts changed after rejected price ops: %d/%d/%d vs %d/%d/%d", h, c, p, hCount, cCount2, pCount2)
	}
	_ = cCount
	_ = pCount

	// Search and heading filter on GET /api/contacts.
	rec = f.do(http.MethodGet, "/api/contacts?headingId="+headings["gazebos"]+"&q=Yerba", "", ip, officer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list filtered = %d body=%s", rec.Code, rec.Body)
	}
	var listed []contactDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || len(listed) != 1 || len(listed[0].ReferencePrices) != 1 {
		t.Fatalf("listed = %+v, err=%v", listed, err)
	}

	// Edit contact (R5), including validation rejection leaving row intact.
	wantResponse(t, f.do(http.MethodPatch, "/api/contacts/"+contact.ID, `{"email":"bad"}`, ip, officer, nil), http.StatusUnprocessableEntity, `{"error":"invalid_email"}`)
	rec = f.do(http.MethodPatch, "/api/contacts/"+contact.ID, `{"phone":"381 555-9999","zone":"San Miguel de Tucumán"}`, ip, officer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch contact = %d body=%s", rec.Code, rec.Body)
	}
	var updated contactDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Phone != "381 555-9999" || updated.Zone != "San Miguel de Tucumán" {
		t.Fatalf("updated contact = %+v", updated)
	}

	// Delete reference price (R4) and contact (R5).
	wantResponse(t, f.do(http.MethodDelete, "/api/contacts/"+contact.ID+"/reference-prices/"+price.ID, "", ip, officer, nil), http.StatusNoContent, "")
	wantResponse(t, f.do(http.MethodDelete, "/api/contacts/"+contact.ID+"/reference-prices/"+price.ID, "", ip, officer, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, f.do(http.MethodDelete, "/api/contacts/"+contact.ID, "", ip, officer, nil), http.StatusNoContent, "")
	wantResponse(t, f.do(http.MethodPatch, "/api/contacts/"+contact.ID, `{"name":"X"}`, ip, officer, nil), http.StatusNotFound, `{"error":"not_found"}`)
}

func TestContactsDirectoryPersistsAcrossBoards(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	const ip = "198.51.100.32"

	// Sign in two students with MFA and link them to roster members.
	memberA := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	memberB := seedMember(t, ctx, f.tenant, "Beto Ficticio", "30111223")
	userA, tokenA, _, _ := f.signInMFA(t, ctx, "student")
	userB, tokenB, _, _ := f.signInMFA(t, ctx, "student")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, memberA, f.tenant, userA); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, memberB, f.tenant, userB); err != nil {
		t.Fatal(err)
	}

	wantResponse(t, f.do(http.MethodPost, "/api/members/"+memberA+"/officer", `{"position":"Presidente"}`, ip, admin, nil), http.StatusCreated, "")
	rec := f.do(http.MethodPost, "/api/contacts", `{"name":"Sonido Ficticio","headingId":"`+headings["sonido e iluminación"]+`","phone":"381 555-1234"}`, ip, tokenA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("officer A create contact = %d body=%s", rec.Code, rec.Body)
	}
	var created contactDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if r := f.do(http.MethodPost, "/api/contacts/"+created.ID+"/reference-prices", `{"description":"Sonido básico","amountCents":1500000,"date":"2026-10-01"}`, ip, tokenA, nil); r.Code != http.StatusCreated {
		t.Fatalf("add price = %d body=%s", r.Code, r.Body)
	}

	// Board changes: Officer A leaves, Officer B takes office.
	wantResponse(t, f.do(http.MethodDelete, "/api/members/"+memberA+"/officer", "", ip, admin, nil), http.StatusNoContent, "")
	wantResponse(t, f.do(http.MethodPost, "/api/members/"+memberB+"/officer", `{"position":"Presidente"}`, ip, admin, nil), http.StatusCreated, "")

	// Former Officer A is now a Student -> 403 forbidden.
	wantResponse(t, f.do(http.MethodGet, "/api/contacts", "", ip, tokenA, nil), http.StatusForbidden, `{"error":"forbidden"}`)

	// New Officer B sees the exact same contact and reference price.
	rec = f.do(http.MethodGet, "/api/contacts", "", ip, tokenB, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("officer B list = %d body=%s", rec.Code, rec.Body)
	}
	var list []contactDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].ID != created.ID || len(list[0].ReferencePrices) != 1 {
		t.Fatalf("officer B contacts = %+v, err=%v", list, err)
	}
}

func TestContactsAuthorizationMatrixAndIsolation(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officerMFA, _, _ := f.signInMFA(t, ctx, "officer")
	_, officerNoMFA := f.signIn(t, ctx, "officer")
	_, adminMFA, _, _ := f.signInMFA(t, ctx, "school_admin")
	_, adminNoMFA := f.signIn(t, ctx, "school_admin")
	_, student := f.signIn(t, ctx, "student")
	_, outsider := f.signIn(t, ctx, "")
	const ip = "198.51.100.33"

	// Seed one contact and price in this tenant.
	rec := f.do(http.MethodPost, "/api/contacts", `{"name":"Local","headingId":"`+headings["gazebos"]+`"}`, ip, officerMFA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var localContact contactDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &localContact)
	rec = f.do(http.MethodPost, "/api/contacts/"+localContact.ID+"/reference-prices", `{"description":"Base","amountCents":10000,"date":"2026-09-01"}`, ip, officerMFA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var localPrice referencePriceDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &localPrice)

	// Seed another tenant's heading, contact, and reference price for RLS isolation test (R8).
	other := newTenant(t, ctx)
	otherHeadings := seedHeadingsForTenant(t, ctx, other)
	var foreignContact, foreignPrice string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO contacts (tenant_id, heading_id, name) VALUES ($1, $2, 'Ajeno') RETURNING id::text`, other, otherHeadings["gazebos"]).Scan(&foreignContact); err != nil {
		t.Fatal(err)
	}
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO contact_reference_prices (tenant_id, contact_id, description, amount_cents, quoted_on) VALUES ($1, $2, 'Ajeno', 5000, '2026-09-01') RETURNING id::text`, other, foreignContact).Scan(&foreignPrice); err != nil {
		t.Fatal(err)
	}

	hCount, cCount, pCount := snapshotDirectory(t, ctx, f.tenant)

	// Role table on read endpoints.
	for _, tc := range []struct {
		name   string
		token  string
		status int
		body   string
	}{
		{"unauthenticated", "", http.StatusUnauthorized, unauthBody},
		{"student", student, http.StatusForbidden, `{"error":"forbidden"}`},
		{"outsider", outsider, http.StatusForbidden, `{"error":"forbidden"}`},
		{"officer without MFA", officerNoMFA, http.StatusForbidden, `{"error":"mfa_required"}`},
		{"admin without MFA", adminNoMFA, http.StatusForbidden, `{"error":"mfa_required"}`},
		{"admin with MFA", adminMFA, http.StatusOK, ""},
		{"officer with MFA", officerMFA, http.StatusOK, ""},
	} {
		t.Run("read/"+tc.name, func(t *testing.T) {
			for _, p := range []string{"/api/contacts", "/api/contacts/headings"} {
				r := f.do(http.MethodGet, p, "", ip, tc.token, nil)
				if tc.status == http.StatusOK {
					if r.Code != http.StatusOK || strings.Contains(r.Body.String(), "Ajeno") {
						t.Fatalf("GET %s = %d body=%s", p, r.Code, r.Body)
					}
				} else {
					wantResponse(t, r, tc.status, tc.body)
				}
			}
		})
	}

	// Role table on every write endpoint: school_admin, student, outsider -> 403 forbidden; officer without MFA -> 403 mfa_required; unauth -> 401.
	writeCalls := []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/contacts/headings", `{"name":"Nuevo"}`},
		{http.MethodPatch, "/api/contacts/headings/" + headings["gazebos"], `{"name":"Renombrado"}`},
		{http.MethodPost, "/api/contacts/headings/" + headings["gazebos"] + "/archive", ""},
		{http.MethodDelete, "/api/contacts/headings/" + headings["artistas"], ""},
		{http.MethodPost, "/api/contacts", `{"name":"Nuevo","headingId":"` + headings["gazebos"] + `"}`},
		{http.MethodPatch, "/api/contacts/" + localContact.ID, `{"name":"Editado"}`},
		{http.MethodDelete, "/api/contacts/" + localContact.ID, ""},
		{http.MethodPost, "/api/contacts/" + localContact.ID + "/reference-prices", `{"description":"X","amountCents":1000,"date":"2026-09-01"}`},
		{http.MethodDelete, "/api/contacts/" + localContact.ID + "/reference-prices/" + localPrice.ID, ""},
	}
	for _, tc := range []struct {
		name   string
		token  string
		status int
		body   string
	}{
		{"unauthenticated", "", http.StatusUnauthorized, unauthBody},
		{"student", student, http.StatusForbidden, `{"error":"forbidden"}`},
		{"outsider", outsider, http.StatusForbidden, `{"error":"forbidden"}`},
		{"admin with MFA", adminMFA, http.StatusForbidden, `{"error":"forbidden"}`},
		{"admin without MFA", adminNoMFA, http.StatusForbidden, `{"error":"forbidden"}`},
		{"officer without MFA", officerNoMFA, http.StatusForbidden, `{"error":"mfa_required"}`},
	} {
		t.Run("write/"+tc.name, func(t *testing.T) {
			for _, w := range writeCalls {
				wantResponse(t, f.do(w.method, w.path, w.body, ip, tc.token, nil), tc.status, tc.body)
			}
		})
	}

	if h, c, p := snapshotDirectory(t, ctx, f.tenant); h != hCount || c != cCount || p != pCount {
		t.Fatalf("directory modified by unauthorized callers: %d/%d/%d vs %d/%d/%d", h, c, p, hCount, cCount, pCount)
	}

	// Cross-tenant isolation: other tenant's heading, contact, price all respond 404 not_found.
	wantResponse(t, f.do(http.MethodPatch, "/api/contacts/headings/"+otherHeadings["gazebos"], `{"name":"Hijack"}`, ip, officerMFA, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts/headings/"+otherHeadings["gazebos"]+"/archive", "", ip, officerMFA, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, f.do(http.MethodDelete, "/api/contacts/headings/"+otherHeadings["gazebos"], "", ip, officerMFA, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, f.do(http.MethodPatch, "/api/contacts/"+foreignContact, `{"name":"Hijack"}`, ip, officerMFA, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, f.do(http.MethodDelete, "/api/contacts/"+foreignContact, "", ip, officerMFA, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, f.do(http.MethodPost, "/api/contacts/"+foreignContact+"/reference-prices", `{"description":"X","amountCents":1000,"date":"2026-09-01"}`, ip, officerMFA, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, f.do(http.MethodDelete, "/api/contacts/"+foreignContact+"/reference-prices/"+foreignPrice, "", ip, officerMFA, nil), http.StatusNotFound, `{"error":"not_found"}`)
}
