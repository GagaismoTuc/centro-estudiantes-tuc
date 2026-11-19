import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { emulateDialog, fail, me, mockApi, noContent, ok, pressEscape, sentTo, settle } from "./test/api.ts";

const sampleHeadings = [
  { id: "h-gaz", name: "gazebos", archived: false, reserved: false },
  { id: "h-art", name: "artistas", archived: false, reserved: false },
];

const contactsFixture = [
  {
    id: "c-1",
    headingId: "h-gaz",
    headingName: "gazebos",
    headingArchived: false,
    name: "Gazebos Ficticios SRL",
    phone: "381 555-0000",
    email: "ventas@gazebos-ficticios.example",
    zone: "Yerba Buena",
    notes: "Pagan tarde, pedir seña",
    referencePrices: [
      {
        id: "p-1",
        description: "Gazebo 3×3",
        amountCents: 2500000,
        date: "2026-09-01",
        createdAt: "2026-09-01T12:00:00Z",
      },
    ],
    createdAt: "2026-09-01T12:00:00Z",
    updatedAt: "2026-09-01T12:00:00Z",
  },
  {
    id: "c-2",
    headingId: "h-art",
    headingName: "artistas",
    headingArchived: false,
    name: "Banda Rechazada",
    phone: "381 555-0199",
    email: "",
    zone: "San Miguel de Tucumán",
    notes: "",
    referencePrices: [],
    shareStatus: "rejected" as const,
    createdAt: "2026-09-02T12:00:00Z",
    updatedAt: "2026-09-02T12:00:00Z",
  },
];

const sharedPage1 = {
  items: [
    {
      id: "sc-1",
      headingName: "gazebos",
      name: "Gazebos Ficticios SRL",
      phone: "381 555-0000",
      email: "ventas@gazebos-ficticios.example",
      zone: "Yerba Buena",
      referencePrices: [{ description: "Gazebo 3×3", amountCents: 2500000, date: "2026-09-01" }],
      createdAt: "2026-09-01T12:00:00Z",
    },
  ],
  headings: ["artistas", "gazebos"],
  page: 1,
  pageSize: 50,
  total: 55,
  totalPages: 2,
};

const sharedFiltered = {
  items: sharedPage1.items,
  headings: ["artistas", "gazebos"],
  page: 1,
  pageSize: 50,
  total: 1,
  totalPages: 1,
};

beforeEach(() => {
  emulateDialog();
});

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
});

describe("Shared directory and contact sharing", () => {
  test("officer shares a contact from Contactos, sees Pendiente de aprobación, and can re-share a Rechazado contact", async () => {
    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" })),
      "GET /contacts/headings": ok(sampleHeadings),
      "GET /contacts": ok(contactsFixture),
      "POST /contacts/c-1/share": ok({ id: "sc-new", status: "pending", createdAt: "2026-09-10T12:00:00Z" }, 201),
      "POST /contacts/c-2/share": fail(409, "already_shared"),
    });

    window.history.replaceState(null, "", "/contactos");
    render(<App />);

    const card1 = await screen.findByTestId("contact-c-1");
    const card2 = screen.getByTestId("contact-c-2");

    // Rejected contact shows "Rechazado" and still offers "Compartir".
    expect(within(card2).getByText("Rechazado")).toBeTruthy();
    expect(within(card2).getByRole("button", { name: "Compartir" })).toBeTruthy();

    // Esc cancels share dialog without sending POST.
    fireEvent.click(within(card1).getByRole("button", { name: "Compartir" }));
    const dialog = await screen.findByRole("dialog");
    pressEscape(dialog);
    await settle();
    expect(sentTo(sent, "POST /contacts/c-1/share")).toHaveLength(0);

    // Confirming share sends POST, updates badge to "Pendiente de aprobación", and focuses status.
    fireEvent.click(within(card1).getByRole("button", { name: "Compartir" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Compartir" }));
    expect(await within(card1).findByText("Pendiente de aprobación")).toBeTruthy();
    expect(within(card1).queryByRole("button", { name: "Compartir" })).toBeNull();
    expect(sentTo(sent, "POST /contacts/c-1/share")).toHaveLength(1);
    await waitFor(() => {
      expect(document.activeElement?.classList.contains("form-status")).toBe(true);
    });

    // Server conflict surfaces Spanish error in status alert and focuses it.
    fireEvent.click(within(card2).getByRole("button", { name: "Compartir" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Compartir" }));
    await screen.findByText(/ya está compartido o pendiente de aprobación/i);
    await waitFor(() => {
      expect(document.activeElement?.classList.contains("form-status")).toBe(true);
    });
  });

  test("officer, school_admin, and veedor read Directorio compartido with Rubro/zone filters and pagination", async () => {
    const sharedPage2 = {
      ...sharedPage1,
      items: [
        {
          id: "sc-51",
          headingName: "artistas",
          name: "Banda Página 2",
          phone: "",
          email: "",
          zone: "Concepción",
          referencePrices: [],
          createdAt: "2026-09-02T12:00:00Z",
        },
      ],
      page: 2,
    };

    mockApi({
      "GET /auth/me": ok(me({ role: "school_admin", roles: ["school_admin"], mfa: "satisfied" })),
      "GET /shared-contacts": [ok(sharedPage1), ok(sharedPage2), ok(sharedFiltered), ok(sharedFiltered)],
    });

    window.history.replaceState(null, "", "/contactos/compartido");
    render(<App />);

    expect(await screen.findByRole("heading", { name: "Directorio compartido" })).toBeTruthy();
    expect(await screen.findByText("Gazebos Ficticios SRL")).toBeTruthy();
    expect(screen.getByText("$25.000")).toBeTruthy();
    expect(screen.getByText("Página 1 de 2")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Editar|Borrar|Compartir/i })).toBeNull();

    // Pagination next page.
    fireEvent.click(screen.getByRole("button", { name: "Siguiente" }));
    expect(await screen.findByText("Banda Página 2")).toBeTruthy();

    // Filter by Rubro and zone.
    fireEvent.change(screen.getByLabelText("Rubro"), { target: { value: "gazebos" } });
    fireEvent.change(screen.getByLabelText("Zona o ciudad"), { target: { value: "Yerba Buena" } });
    await waitFor(() => {
      const urls = vi.mocked(fetch).mock.calls.map((c) => new URL((c[0] as Request).url));
      expect(
        urls.some(
          (u) =>
            u.pathname === "/api/shared-contacts" &&
            u.searchParams.get("heading") === "gazebos" &&
            u.searchParams.get("zone") === "Yerba Buena",
        ),
      ).toBe(true);
    });

    // Veedor also has read-only access to /contactos/compartido.
    cleanup();
    mockApi({
      "GET /auth/me": ok(me({ role: "veedor" as never, roles: ["veedor" as never], mfa: "satisfied" })),
      "GET /shared-contacts": ok(sharedFiltered),
    });
    window.history.replaceState(null, "", "/contactos/compartido");
    render(<App />);
    expect(await screen.findByText("Gazebos Ficticios SRL")).toBeTruthy();
  });

  test("students cannot see Directorio compartido in nav or access /contactos/compartido", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "student", roles: ["student"], mfa: "not_required" })) });

    window.history.replaceState(null, "", "/contactos/compartido");
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Directorio compartido" })).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("No tenés acceso a esta sección");
    expect(screen.queryByRole("link", { name: "Directorio compartido" })).toBeNull();
  });

  test('R1: officer copies a shared contact to own directory via "Copiar a nuestros contactos" and surfaces not_found errors', async () => {
    const sharedTwo = {
      ...sharedFiltered,
      items: [
        sharedPage1.items[0]!,
        {
          id: "sc-2",
          headingName: "sonido",
          name: "Sonido Dado de Baja",
          phone: "381 555-9999",
          email: "",
          zone: "Tafí Viejo",
          referencePrices: [],
          createdAt: "2026-09-03T12:00:00Z",
        },
      ],
      total: 2,
    };
    const copiedContact = {
      id: "c-copy-1",
      headingId: "h-otr",
      headingName: "otros",
      headingArchived: false,
      name: "Gazebos Ficticios SRL",
      phone: "381 555-0000",
      email: "ventas@gazebos-ficticios.example",
      zone: "Yerba Buena",
      notes: "",
      referencePrices: [
        {
          id: "p-copy-1",
          description: "Gazebo 3×3",
          amountCents: 2500000,
          date: "2026-09-01",
          createdAt: "2026-09-12T12:00:00Z",
        },
      ],
      createdAt: "2026-09-12T12:00:00Z",
      updatedAt: "2026-09-12T12:00:00Z",
    };

    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" })),
      "GET /shared-contacts": ok(sharedTwo),
      "POST /shared-contacts/sc-1/copy": [
        ok(copiedContact, 201),
        ok({ ...copiedContact, id: "c-copy-2" }, 201),
      ],
      "POST /shared-contacts/sc-2/copy": fail(404, "not_found"),
    });

    window.history.replaceState(null, "", "/contactos/compartido");
    render(<App />);

    const card1 = await screen.findByTestId("shared-contact-sc-1");
    const card2 = screen.getByTestId("shared-contact-sc-2");

    // Officer copies sc-1 -> falls back to heading "otros" and confirms in live status.
    const copyBtn1 = within(card1).getByRole("button", { name: "Copiar a nuestros contactos" });
    fireEvent.click(copyBtn1);
    expect(await screen.findByText(/Se copió a Gazebos Ficticios SRL.*otros/i)).toBeTruthy();
    expect(sentTo(sent, "POST /shared-contacts/sc-1/copy")).toHaveLength(1);

    // Copying twice is allowed (R2: no deduplication).
    fireEvent.click(within(card1).getByRole("button", { name: "Copiar a nuestros contactos" }));
    await waitFor(() => {
      expect(sentTo(sent, "POST /shared-contacts/sc-1/copy")).toHaveLength(2);
    });

    // Negative: unapproved/removed/nonexistent shared contact returns 404 not_found -> Spanish alert + focus.
    fireEvent.click(within(card2).getByRole("button", { name: "Copiar a nuestros contactos" }));
    expect(await screen.findByText("No encontramos lo que buscabas.")).toBeTruthy();
    await waitFor(() => {
      expect(document.activeElement?.classList.contains("form-status")).toBe(true);
    });

    // Non-officers (school_admin, veedor) cannot see "Copiar a nuestros contactos" (R6).
    for (const role of ["school_admin", "veedor"] as const) {
      cleanup();
      mockApi({
        "GET /auth/me": ok(me({ role: role as never, roles: [role as never], mfa: "satisfied" })),
        "GET /shared-contacts": ok(sharedFiltered),
      });
      window.history.replaceState(null, "", "/contactos/compartido");
      render(<App />);
      expect(await screen.findByText("Gazebos Ficticios SRL")).toBeTruthy();
      expect(screen.queryByRole("button", { name: "Copiar a nuestros contactos" })).toBeNull();
    }
  });

  test('R3: officer withdraws an approved or pending shared contact via "Dejar de compartir" with ConfirmDialog and handles not_shared error', async () => {
    const sharedContacts = [
      {
        ...contactsFixture[0]!,
        id: "c-approved",
        name: "Gazebos Aprobado",
        shareStatus: "approved" as const,
        hasApprovedShare: true,
        shareOutdated: true,
      },
      {
        ...contactsFixture[1]!,
        id: "c-pending",
        name: "Banda Pendiente",
        shareStatus: "pending" as const,
        hasApprovedShare: false,
      },
      {
        ...contactsFixture[0]!,
        id: "c-error",
        name: "Sonido Error Retiro",
        shareStatus: "approved" as const,
        hasApprovedShare: true,
      },
    ];

    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" })),
      "GET /contacts/headings": ok(sampleHeadings),
      "GET /contacts": ok(sharedContacts),
      "DELETE /contacts/c-approved/share": noContent(),
      "DELETE /contacts/c-pending/share": noContent(),
      "DELETE /contacts/c-error/share": fail(404, "not_shared"),
    });

    window.history.replaceState(null, "", "/contactos");
    render(<App />);

    const cardApproved = await screen.findByTestId("contact-c-approved");
    const cardPending = screen.getByTestId("contact-c-pending");
    const cardError = screen.getByTestId("contact-c-error");

    expect(within(cardApproved).getByText("Compartido")).toBeTruthy();
    expect(within(cardApproved).getByText("La versión compartida es anterior a tus cambios")).toBeTruthy();

    // Esc cancels ConfirmDialog without sending DELETE.
    fireEvent.click(within(cardApproved).getByRole("button", { name: "Dejar de compartir" }));
    const dialog = await screen.findByRole("dialog");
    pressEscape(dialog);
    await settle();
    expect(sentTo(sent, "DELETE /contacts/c-approved/share")).toHaveLength(0);

    // Confirming withdraws approved contact, clears badge + R4 notice + withdraw/reshare buttons, restores "Compartir", and focuses status.
    fireEvent.click(within(cardApproved).getByRole("button", { name: "Dejar de compartir" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Dejar de compartir" }));
    await waitFor(() => {
      expect(sentTo(sent, "DELETE /contacts/c-approved/share")).toHaveLength(1);
    });
    expect(within(cardApproved).queryByText("Compartido")).toBeNull();
    expect(within(cardApproved).queryByText("La versión compartida es anterior a tus cambios")).toBeNull();
    expect(within(cardApproved).queryByRole("button", { name: "Dejar de compartir" })).toBeNull();
    expect(within(cardApproved).queryByRole("button", { name: "Compartir de nuevo" })).toBeNull();
    expect(within(cardApproved).getByRole("button", { name: "Compartir" })).toBeTruthy();
    await waitFor(() => {
      expect(document.activeElement?.classList.contains("form-status")).toBe(true);
    });

    // Pending contact can also be withdrawn.
    expect(within(cardPending).getByText("Pendiente de aprobación")).toBeTruthy();
    fireEvent.click(within(cardPending).getByRole("button", { name: "Dejar de compartir" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Dejar de compartir" }));
    await waitFor(() => {
      expect(sentTo(sent, "DELETE /contacts/c-pending/share")).toHaveLength(1);
    });
    expect(within(cardPending).queryByText("Pendiente de aprobación")).toBeNull();
    expect(within(cardPending).queryByRole("button", { name: "Dejar de compartir" })).toBeNull();
    expect(within(cardPending).getByRole("button", { name: "Compartir" })).toBeTruthy();

    // Negative: 404 not_shared shows Spanish message, focuses status, and leaves contact unchanged.
    fireEvent.click(within(cardError).getByRole("button", { name: "Dejar de compartir" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Dejar de compartir" }));
    expect(await screen.findByText("Este contacto no está compartido en este momento.")).toBeTruthy();
    await waitFor(() => {
      expect(document.activeElement?.classList.contains("form-status")).toBe(true);
    });
    expect(within(cardError).getByText("Compartido")).toBeTruthy();
    expect(within(cardError).getByRole("button", { name: "Dejar de compartir" })).toBeTruthy();

    // Non-officer (school_admin) does not see "Dejar de compartir" or "Compartir de nuevo" (R6).
    cleanup();
    mockApi({
      "GET /auth/me": ok(me({ role: "school_admin", roles: ["school_admin"], mfa: "satisfied" })),
      "GET /contacts/headings": ok(sampleHeadings),
      "GET /contacts": ok([sharedContacts[0]!]),
    });
    window.history.replaceState(null, "", "/contactos");
    render(<App />);
    const readOnlyCard = await screen.findByTestId("contact-c-approved");
    expect(within(readOnlyCard).getByText("Compartido")).toBeTruthy();
    expect(within(readOnlyCard).queryByRole("button", { name: /Dejar de compartir|Compartir de nuevo/i })).toBeNull();
  });

  test('R4: editing an approved shared contact shows "La versión compartida es anterior a tus cambios"', async () => {
    const approvedContact = {
      ...contactsFixture[0]!,
      id: "c-edit",
      shareStatus: "approved" as const,
      hasApprovedShare: true,
      shareOutdated: false,
    };
    const editedContact = {
      ...approvedContact,
      phone: "381 222-2222",
      shareOutdated: true,
    };
    const priceContact = {
      ...contactsFixture[1]!,
      id: "c-price",
      name: "Banda Con Precio",
      shareStatus: "approved" as const,
      hasApprovedShare: true,
      shareOutdated: false,
    };
    const addedPrice = {
      id: "p-new",
      description: "Show 2 horas",
      amountCents: 9000000,
      date: "2026-09-10",
      createdAt: "2026-09-10T12:00:00Z",
    };

    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" })),
      "GET /contacts/headings": ok(sampleHeadings),
      "GET /contacts": ok([approvedContact, priceContact]),
      "PATCH /contacts/c-edit": ok(editedContact),
      "POST /contacts/c-price/reference-prices": ok(addedPrice, 201),
    });

    window.history.replaceState(null, "", "/contactos");
    render(<App />);

    const cardEdit = await screen.findByTestId("contact-c-edit");
    expect(within(cardEdit).getByText("Compartido")).toBeTruthy();
    expect(within(cardEdit).queryByText("La versión compartida es anterior a tus cambios")).toBeNull();

    // Edit phone -> PATCH returns shareOutdated: true -> R4 warning appears while keeping "Compartido".
    fireEvent.click(within(cardEdit).getByRole("button", { name: "Editar" }));
    const editForm = screen.getByRole("form", { name: "Editar Gazebos Ficticios SRL" });
    fireEvent.change(within(editForm).getByLabelText("Teléfono / WhatsApp"), { target: { value: "381 222-2222" } });
    fireEvent.submit(editForm);

    const updatedCard = await screen.findByTestId("contact-c-edit");
    expect(within(updatedCard).getByText("381 222-2222")).toBeTruthy();
    expect(within(updatedCard).getByText("Compartido")).toBeTruthy();
    expect(within(updatedCard).getByText("La versión compartida es anterior a tus cambios")).toBeTruthy();
    expect(sentTo(sent, "PATCH /contacts/c-edit")).toHaveLength(1);

    // Adding a reference price to an approved shared contact also flags R4 warning locally.
    const cardPrice = screen.getByTestId("contact-c-price");
    expect(within(cardPrice).queryByText("La versión compartida es anterior a tus cambios")).toBeNull();
    fireEvent.click(within(cardPrice).getByRole("button", { name: "Agregar precio de referencia" }));
    const priceForm = within(cardPrice).getByRole("form", { name: "Nuevo precio de referencia" });
    fireEvent.change(within(priceForm).getByLabelText("Descripción"), { target: { value: "Show 2 horas" } });
    fireEvent.change(within(priceForm).getByLabelText("Monto ($)"), { target: { value: "90000" } });
    fireEvent.change(within(priceForm).getByLabelText("Fecha"), { target: { value: "2026-09-10" } });
    fireEvent.submit(priceForm);

    expect(await within(cardPrice).findByText("La versión compartida es anterior a tus cambios")).toBeTruthy();
  });

  test('R5: officer re-shares an approved contact via "Compartir de nuevo", handles already_pending, and preserves "Compartido" when a re-share is rejected', async () => {
    const reshareContacts = [
      {
        ...contactsFixture[0]!,
        id: "c-outdated",
        name: "Gazebos Editado",
        shareStatus: "approved" as const,
        hasApprovedShare: true,
        shareOutdated: true,
      },
      {
        ...contactsFixture[1]!,
        id: "c-rejected-v2",
        name: "Banda Recompartida Rechazada",
        shareStatus: "rejected" as const,
        hasApprovedShare: true,
        shareOutdated: true,
      },
      {
        ...contactsFixture[0]!,
        id: "c-conflict",
        name: "Sonido Conflicto",
        shareStatus: "approved" as const,
        hasApprovedShare: true,
        shareOutdated: true,
      },
    ];

    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" })),
      "GET /contacts/headings": ok(sampleHeadings),
      "GET /contacts": ok(reshareContacts),
      "POST /contacts/c-outdated/share": ok({ id: "sc-v2", status: "pending", createdAt: "2026-09-15T12:00:00Z" }, 201),
      "POST /contacts/c-conflict/share": fail(409, "already_pending"),
    });

    window.history.replaceState(null, "", "/contactos");
    render(<App />);

    const cardOutdated = await screen.findByTestId("contact-c-outdated");
    const cardRejectedV2 = screen.getByTestId("contact-c-rejected-v2");
    const cardConflict = screen.getByTestId("contact-c-conflict");

    // Pi's review note from #106: when a re-share is rejected while v1 is still approved,
    // shareStatus is "rejected" and hasApprovedShare is true -> must still show "Compartido",
    // distinguish the rejected new version, and allow both "Compartir de nuevo" and "Dejar de compartir".
    expect(within(cardRejectedV2).getByText("Compartido")).toBeTruthy();
    expect(within(cardRejectedV2).getByText("Nueva versión rechazada")).toBeTruthy();
    expect(within(cardRejectedV2).queryByText("Rechazado")).toBeNull();
    expect(within(cardRejectedV2).getByText("La versión compartida es anterior a tus cambios")).toBeTruthy();
    expect(within(cardRejectedV2).getByRole("button", { name: "Compartir de nuevo" })).toBeTruthy();
    expect(within(cardRejectedV2).getByRole("button", { name: "Dejar de compartir" })).toBeTruthy();
    expect(within(cardRejectedV2).queryByRole("button", { name: "Compartir" })).toBeNull();

    // Esc cancels re-share dialog without sending POST.
    fireEvent.click(within(cardOutdated).getByRole("button", { name: "Compartir de nuevo" }));
    const dialog = await screen.findByRole("dialog");
    pressEscape(dialog);
    await settle();
    expect(sentTo(sent, "POST /contacts/c-outdated/share")).toHaveLength(0);

    // Confirming "Compartir de nuevo" sends POST, keeps "Compartido" visible alongside pending new version,
    // hides "Compartir de nuevo", keeps "Dejar de compartir", and focuses status.
    fireEvent.click(within(cardOutdated).getByRole("button", { name: "Compartir de nuevo" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Compartir de nuevo" }));
    expect(await within(cardOutdated).findByText("Nueva versión pendiente de aprobación")).toBeTruthy();
    expect(within(cardOutdated).getByText("Compartido")).toBeTruthy();
    expect(within(cardOutdated).queryByRole("button", { name: "Compartir de nuevo" })).toBeNull();
    expect(within(cardOutdated).getByRole("button", { name: "Dejar de compartir" })).toBeTruthy();
    expect(sentTo(sent, "POST /contacts/c-outdated/share")).toHaveLength(1);
    await waitFor(() => {
      expect(document.activeElement?.classList.contains("form-status")).toBe(true);
    });

    // Negative: 409 already_pending surfaces Spanish error, focuses status, and leaves contact unchanged.
    fireEvent.click(within(cardConflict).getByRole("button", { name: "Compartir de nuevo" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Compartir de nuevo" }));
    expect(await screen.findByText("Este contacto ya tiene una solicitud pendiente de aprobación.")).toBeTruthy();
    await waitFor(() => {
      expect(document.activeElement?.classList.contains("form-status")).toBe(true);
    });
    expect(within(cardConflict).getByText("Compartido")).toBeTruthy();
    expect(within(cardConflict).queryByText("Nueva versión pendiente de aprobación")).toBeNull();
    expect(within(cardConflict).getByRole("button", { name: "Compartir de nuevo" })).toBeTruthy();
  });
});
