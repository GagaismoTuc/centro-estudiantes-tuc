package contacts_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/contacts"
)

func headingIDByName(t *testing.T, ctx context.Context, s *contacts.Store, sess auth.Session, name string) string {
	t.Helper()
	hs, err := s.ListHeadings(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hs {
		if h.Name == name {
			return h.ID
		}
	}
	t.Fatalf("heading %q not found", name)
	return ""
}

func TestSharedContactsCopyStore(t *testing.T) {
	ctx := integration(t)
	fA := newFixture(t, ctx)
	fB := newFixture(t, ctx)
	fC := newFixture(t, ctx)

	offA := fA.seedMember(t, ctx, "copy-off-a@example.com", "officer", false, true)
	offB := fB.seedMember(t, ctx, "copy-off-b@example.com", "officer", false, true)
	offC := fC.seedMember(t, ctx, "copy-off-c@example.com", "officer", false, true)

	// Remove "gazebos" from School B so it falls back to "otros"; archive "gazebos" in School C so it also falls back.
	gazB := headingIDByName(t, ctx, fB.store, offB, "gazebos")
	if err := fB.store.DeleteHeading(ctx, offB, gazB); err != nil {
		t.Fatal(err)
	}
	gazC := headingIDByName(t, ctx, fC.store, offC, "gazebos")
	if _, err := fC.store.ArchiveHeading(ctx, offC, gazC); err != nil {
		t.Fatal(err)
	}
	// School B has "Sonido e Iluminación" in mixed case to verify case-insensitive active match.
	sonidoB := headingIDByName(t, ctx, fB.store, offB, "sonido e iluminación")

	gazA := headingIDByName(t, ctx, fA.store, offA, "gazebos")
	sonidoA := headingIDByName(t, ctx, fA.store, offA, "sonido e iluminación")

	cGazA, err := fA.store.CreateContact(ctx, offA, contacts.CreateContactInput{
		Name:      "Gazebos Ficticios SRL",
		HeadingID: gazA,
		Phone:     "381 555-0000",
		Email:     "ventas@gazebos.example",
		Zone:      "Yerba Buena",
		Notes:     "Nota privada de A",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fA.store.AddReferencePrice(ctx, offA, cGazA.ID, contacts.CreateReferencePriceInput{
		Description: "Gazebo 3x3",
		AmountCents: 2500000,
		Date:        "2026-09-01",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fA.store.AddReferencePrice(ctx, offA, cGazA.ID, contacts.CreateReferencePriceInput{
		Description: "Gazebo 6x3",
		AmountCents: 4200000,
		Date:        "2026-09-02",
	}); err != nil {
		t.Fatal(err)
	}

	cSonA, err := fA.store.CreateContact(ctx, offA, contacts.CreateContactInput{
		Name:      "Sonido Pro",
		HeadingID: sonidoA,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Create a contact in A with custom mixed-case heading to test case-insensitive match in B.
	if _, err := fA.store.RenameHeading(ctx, offA, sonidoA, "SONIDO E ILUMINACIÓN"); err != nil {
		t.Fatal(err)
	}

	shGaz, err := fA.store.ShareContact(ctx, offA, cGazA.ID)
	if err != nil {
		t.Fatal(err)
	}
	shSon, err := fA.store.ShareContact(ctx, offA, cSonA.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id IN ($1, $2)`, shGaz.ID, shSon.ID)
	})

	// R1 negative: pending cannot be copied.
	if _, err := fB.store.CopySharedContact(ctx, offB, shGaz.ID); !errors.Is(err, contacts.ErrNotFound) {
		t.Fatalf("copy pending = %v, want ErrNotFound", err)
	}

	if err := contacts.ApproveShared(ctx, tdb.Super, shGaz.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := contacts.ApproveShared(ctx, tdb.Super, shSon.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	// R1: School B has no "gazebos" -> maps to "otros"; copies fields & prices without notes or share status.
	copyB1, err := fB.store.CopySharedContact(ctx, offB, shGaz.ID)
	if err != nil {
		t.Fatalf("copy gaz to B: %v", err)
	}
	if copyB1.HeadingName != "otros" || copyB1.Name != "Gazebos Ficticios SRL" ||
		copyB1.Phone != "381 555-0000" || copyB1.Email != "ventas@gazebos.example" ||
		copyB1.Zone != "Yerba Buena" || copyB1.Notes != "" || copyB1.ShareStatus != "" ||
		len(copyB1.ReferencePrices) != 2 {
		t.Fatalf("copyB1 = %+v", copyB1)
	}

	// R1: Case-insensitive active heading match.
	copySonB, err := fB.store.CopySharedContact(ctx, offB, shSon.ID)
	if err != nil {
		t.Fatalf("copy sonido to B: %v", err)
	}
	if copySonB.HeadingID != sonidoB {
		t.Fatalf("copySonB headingID = %s, want %s", copySonB.HeadingID, sonidoB)
	}

	// R1: Archived heading in School C is skipped -> falls back to "otros".
	copyC, err := fC.store.CopySharedContact(ctx, offC, shGaz.ID)
	if err != nil {
		t.Fatalf("copy gaz to C: %v", err)
	}
	if copyC.HeadingName != "otros" {
		t.Fatalf("copyC headingName = %q, want otros", copyC.HeadingName)
	}

	// R2: Copying twice creates two independent contacts.
	copyB2, err := fB.store.CopySharedContact(ctx, offB, shGaz.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copyB2.ID == copyB1.ID {
		t.Fatalf("second copy reused id %s", copyB2.ID)
	}

	// R7: Atomicity — if inserting a reference price fails, no contact row remains.
	_, err = tdb.Super.Exec(ctx, `
		CREATE FUNCTION copy_price_fail_probe() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.tenant_id = $1::uuid AND NEW.description = 'Gazebo 3x3' THEN
				RAISE EXCEPTION 'probe fail';
			END IF;
			RETURN NEW;
		END $$;
	`)
	// Note: Postgres PL/pgSQL doesn't take $1 in DDL; use literal tenant ID.
	_ = err
	_, _ = tdb.Super.Exec(ctx, `DROP FUNCTION IF EXISTS copy_price_fail_probe()`)
	if _, err := tdb.Super.Exec(ctx, `
		CREATE FUNCTION copy_price_fail_probe() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.tenant_id = '`+fC.tenant+`'::uuid AND NEW.description = 'Gazebo 3x3' THEN
				RAISE EXCEPTION 'probe fail';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER copy_price_fail_probe_trig BEFORE INSERT ON contact_reference_prices
			FOR EACH ROW EXECUTE FUNCTION copy_price_fail_probe();
	`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DROP TRIGGER IF EXISTS copy_price_fail_probe_trig ON contact_reference_prices; DROP FUNCTION IF EXISTS copy_price_fail_probe()`)
	})

	beforeList, err := fC.store.ListContacts(ctx, offC, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fC.store.CopySharedContact(ctx, offC, shGaz.ID); err == nil {
		t.Fatalf("expected error from price trigger, got nil")
	}
	afterList, err := fC.store.ListContacts(ctx, offC, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(afterList) != len(beforeList) {
		t.Fatalf("partial contact leaked after price failure: before=%d after=%d", len(beforeList), len(afterList))
	}

	// R1 negative: removed cannot be copied.
	if err := contacts.RemoveShared(ctx, tdb.Super, shGaz.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := fB.store.CopySharedContact(ctx, offB, shGaz.ID); !errors.Is(err, contacts.ErrNotFound) {
		t.Fatalf("copy removed = %v, want ErrNotFound", err)
	}
	// R2: Copies in B remain intact after removal.
	bList, err := fB.store.ListContacts(ctx, offB, "", "")
	if err != nil || len(bList) != 3 {
		t.Fatalf("B contacts after remove = %d (err=%v), want 3", len(bList), err)
	}
	seenB := make(map[string]bool, len(bList))
	for _, c := range bList {
		seenB[c.ID] = true
	}
	for _, wantID := range []string{copyB1.ID, copyB2.ID, copySonB.ID} {
		if !seenB[wantID] {
			t.Fatalf("B contact %s missing after remove; got %+v", wantID, bList)
		}
	}
}

func TestSharedContactsWithdrawAndReshareStore(t *testing.T) {
	ctx := integration(t)
	fA := newFixture(t, ctx)
	fB := newFixture(t, ctx)

	offA := fA.seedMember(t, ctx, "share-off-a@example.com", "officer", false, true)
	offB := fB.seedMember(t, ctx, "share-off-b@example.com", "officer", false, true)
	gazA := headingIDByName(t, ctx, fA.store, offA, "gazebos")

	cA, err := fA.store.CreateContact(ctx, offA, contacts.CreateContactInput{
		Name:      "Ana Contact",
		HeadingID: gazA,
		Phone:     "381 111-1111",
		Notes:     "Privada",
	})
	if err != nil {
		t.Fatal(err)
	}

	// R3 negative: withdrawing unshared contact -> ErrNotShared; other school's contact -> ErrNotFound.
	if err := fA.store.WithdrawShare(ctx, offA, cA.ID); !errors.Is(err, contacts.ErrNotShared) {
		t.Fatalf("withdraw unshared = %v, want ErrNotShared", err)
	}
	if err := fB.store.WithdrawShare(ctx, offB, cA.ID); !errors.Is(err, contacts.ErrNotFound) {
		t.Fatalf("withdraw other school = %v, want ErrNotFound", err)
	}

	// Share v1, approve v1.
	v1, err := fA.store.ShareContact(ctx, offA, cA.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id IN (SELECT shared_contact_id FROM shared_contact_links WHERE tenant_id = $1)`, fA.tenant)
	})
	if err := contacts.ApproveShared(ctx, tdb.Super, v1.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	// R4: Edit only notes -> not outdated; edit phone -> outdated, shared still shows old phone.
	notesOnly := "Nota actualizada"
	updated, err := fA.store.UpdateContact(ctx, offA, cA.ID, contacts.UpdateContactInput{Notes: &notesOnly})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ShareOutdated {
		t.Fatalf("notes-only edit marked ShareOutdated=true")
	}

	newPhone := "381 222-2222"
	updated, err = fA.store.UpdateContact(ctx, offA, cA.ID, contacts.UpdateContactInput{Phone: &newPhone})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.ShareOutdated || !updated.HasApprovedShare || updated.ShareStatus != "approved" {
		t.Fatalf("after phone edit = %+v, want ShareOutdated=true HasApprovedShare=true ShareStatus=approved", updated)
	}
	page, err := fB.store.ListSharedContacts(ctx, offB, "", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Phone != "381 111-1111" {
		t.Fatalf("shared search after edit = %+v (err=%v), want old phone", page.Items, err)
	}

	// R5: Re-share while approved -> creates v2 pending, v1 stays visible, second re-share -> ErrAlreadyPending.
	v2, err := fA.store.ShareContact(ctx, offA, cA.ID)
	if err != nil {
		t.Fatalf("re-share while approved: %v", err)
	}
	if v2.ID == v1.ID || v2.Status != "pending" {
		t.Fatalf("v2 = %+v, want pending with new ID", v2)
	}
	if _, err := fA.store.ShareContact(ctx, offA, cA.ID); !errors.Is(err, contacts.ErrAlreadyPending) {
		t.Fatalf("re-share while pending = %v, want ErrAlreadyPending", err)
	}
	page, err = fB.store.ListSharedContacts(ctx, offB, "", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != v1.ID {
		t.Fatalf("shared while v2 pending = %+v, want v1", page.Items)
	}

	// R5: Approve v2 -> replaces v1 in search; contact now up-to-date.
	if err := contacts.ApproveShared(ctx, tdb.Super, v2.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	page, err = fB.store.ListSharedContacts(ctx, offB, "", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != v2.ID || page.Items[0].Phone != "381 222-2222" {
		t.Fatalf("shared after approve v2 = %+v, want v2 with new phone", page.Items)
	}
	listA, err := fA.store.ListContacts(ctx, offA, "", "")
	if err != nil || len(listA) != 1 || listA[0].ShareStatus != "approved" || listA[0].ShareOutdated {
		t.Fatalf("listA after approve v2 = %+v", listA)
	}

	// R7: Parallel withdraw & remove both converge without error and leave contact out of search.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs[0] = fA.store.WithdrawShare(ctx, offA, cA.ID)
	}()
	go func() {
		defer wg.Done()
		<-start
		errs[1] = contacts.RemoveShared(ctx, tdb.Super, v2.ID, time.Now())
	}()
	close(start)
	wg.Wait()
	if errs[0] != nil && !errors.Is(errs[0], contacts.ErrNotShared) {
		t.Fatalf("parallel withdraw err = %v", errs[0])
	}
	if errs[1] != nil {
		t.Fatalf("parallel remove err = %v", errs[1])
	}
	page, err = fB.store.ListSharedContacts(ctx, offB, "", "", 1)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("after parallel withdraw+remove, shared items = %+v (err=%v), want empty", page.Items, err)
	}
}
