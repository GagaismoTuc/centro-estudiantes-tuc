import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import css from "./styles.css?raw";
import { emulateDialog, fail, me, mockApi, noContent, ok, pressEscape, sentTo, settle } from "./test/api.ts";

const sampleHeadings = [
  { id: "h-art", name: "artistas", archived: false, reserved: false },
  { id: "h-gaz", name: "gazebos", archived: false, reserved: false },
  { id: "h-son", name: "sonido e iluminación", archived: false, reserved: false },
  { id: "h-otr", name: "otros", archived: false, reserved: true },
  { id: "h-old", name: "carpas viejas", archived: true, reserved: false },
];

const sampleContacts = [
  {
    id: "c-1",
    headingId: "h-gaz",
    headingName: "gazebos",
    headingArchived: false,
    name: "Gazebos Ficticios SRL",
    phone: "381 555-0000",
    email: "ventas@gazebos-ficticios.example",
    zone: "Yerba Buena",
    notes: "Armado incluido",
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
    name: "Banda Ficticia",
    phone: "381 555-0199",
    email: "",
    zone: "San Miguel de Tucumán",
    notes: "",
    referencePrices: [],
    createdAt: "2026-09-02T12:00:00Z",
    updatedAt: "2026-09-02T12:00:00Z",
  },
];

beforeEach(() => {
  emulateDialog();
});

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
});

describe("Contacts and Headings screens", () => {
  test("students cannot see Contactos in nav or access /contactos or /contactos/rubros", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "student", roles: ["student"], mfa: "not_required" })) });

    window.history.replaceState(null, "", "/contactos");
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Contactos" })).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("No tenés acceso a esta sección");
    expect(screen.queryByRole("link", { name: "Contactos" })).toBeNull();

    cleanup();
    window.history.replaceState(null, "", "/contactos/rubros");
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Rubros" })).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("No tenés acceso a esta sección");
  });

  test("school_admin reads Contactos with filters and cannot mutate or manage Rubros", async () => {
    mockApi({
      "GET /auth/me": ok(me({ role: "school_admin", roles: ["school_admin"], mfa: "satisfied" })),
      "GET /contacts/headings": ok(sampleHeadings),
      "GET /contacts": ok(sampleContacts),
    });

    window.history.replaceState(null, "", "/contactos");
    render(<App />);

    expect(await screen.findByText("Gazebos Ficticios SRL")).toBeTruthy();
    expect(screen.getByText("Banda Ficticia")).toBeTruthy();
    expect(screen.getByText("01/09/2026")).toBeTruthy();
    expect(screen.queryByText("2026-09-01")).toBeNull();
    expect(screen.queryByRole("button", { name: /Nuevo contacto/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /Editar/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /Borrar/i })).toBeNull();
    expect(screen.queryByRole("link", { name: /Manejar rubros/i })).toBeNull();

    // Filter by Rubro and search by name or zone.
    fireEvent.change(screen.getByLabelText("Rubro"), { target: { value: "h-gaz" } });
    expect(screen.getByText("Gazebos Ficticios SRL")).toBeTruthy();
    expect(screen.queryByText("Banda Ficticia")).toBeNull();

    fireEvent.change(screen.getByLabelText("Rubro"), { target: { value: "" } });
    fireEvent.change(screen.getByLabelText("Buscar por nombre o zona"), { target: { value: "yerba" } });
    expect(screen.getByText("Gazebos Ficticios SRL")).toBeTruthy();
    expect(screen.queryByText("Banda Ficticia")).toBeNull();

    cleanup();
    window.history.replaceState(null, "", "/contactos/rubros");
    render(<App />);
    expect((await screen.findByRole("alert")).textContent).toContain("No tenés acceso a esta sección");
  });

  test("officer creates, edits, adds reference price, and deletes contacts with accessible validation and ConfirmDialog", async () => {
    const createdContact = {
      id: "c-3",
      headingId: "h-gaz",
      headingName: "gazebos",
      headingArchived: false,
      name: "Iluminación Tucumán Ficticia",
      phone: "381 555-0142",
      email: "",
      zone: "Tafí Viejo",
      notes: "",
      referencePrices: [],
      createdAt: "2026-09-03T12:00:00Z",
      updatedAt: "2026-09-03T12:00:00Z",
    };
    const editedContact = { ...sampleContacts[0]!, zone: "San Pablo" };
    const addedPrice = {
      id: "p-2",
      description: "Gazebo 6×3",
      amountCents: 1250000,
      date: "2026-09-05",
      createdAt: "2026-09-05T12:00:00Z",
    };
    const withEditAndPrice = [
      { ...editedContact, referencePrices: [addedPrice, ...editedContact.referencePrices] },
      sampleContacts[1]!,
      createdContact,
    ];

    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" })),
      "GET /contacts/headings": ok(sampleHeadings),
      "GET /contacts": [ok(sampleContacts), ok([...sampleContacts, createdContact]), ok([editedContact, sampleContacts[1]!, createdContact]), ok(withEditAndPrice)],
      "POST /contacts": ok(createdContact, 201),
      "PATCH /contacts/c-1": ok(editedContact),
      "POST /contacts/c-1/reference-prices": ok(addedPrice, 201),
      "DELETE /contacts/c-2": noContent(),
    });

    window.history.replaceState(null, "", "/contactos");
    render(<App />);
    expect(await screen.findByText("Gazebos Ficticios SRL")).toBeTruthy();

    // Open create form and test accessible validation (focus + aria-invalid).
    fireEvent.click(screen.getByRole("button", { name: "Nuevo contacto" }));
    const createForm = screen.getByRole("form", { name: "Nuevo contacto" });
    fireEvent.submit(createForm);

    const nameInput = within(createForm).getByLabelText("Nombre");
    await waitFor(() => {
      expect(nameInput.getAttribute("aria-invalid")).toBe("true");
      expect(document.activeElement).toBe(nameInput);
    });
    expect(sentTo(sent, "POST /contacts")).toHaveLength(0);

    // Fill valid fictional contact and submit.
    fireEvent.change(nameInput, { target: { value: "Iluminación Tucumán Ficticia" } });
    fireEvent.change(within(createForm).getByLabelText("Rubro"), { target: { value: "h-gaz" } });
    fireEvent.change(within(createForm).getByLabelText("Teléfono / WhatsApp"), { target: { value: "381 555-0142" } });
    fireEvent.change(within(createForm).getByLabelText("Zona o ciudad"), { target: { value: "Tafí Viejo" } });
    fireEvent.submit(createForm);

    expect(await screen.findByText("Iluminación Tucumán Ficticia")).toBeTruthy();

    // Edit c-1 zone and save.
    fireEvent.click(within(screen.getByTestId("contact-c-1")).getByRole("button", { name: "Editar" }));
    const editForm = screen.getByRole("form", { name: "Editar Gazebos Ficticios SRL" });
    fireEvent.change(within(editForm).getByLabelText("Zona o ciudad"), { target: { value: "San Pablo" } });
    fireEvent.submit(editForm);
    expect(await screen.findByText("San Pablo")).toBeTruthy();
    expect(sentTo(sent, "PATCH /contacts/c-1")).toHaveLength(1);

    // Add reference price to c-1 ($12.500 -> 1250000 cents).
    const card1 = screen.getByTestId("contact-c-1");
    // 23:00 in Argentina is already the next day in UTC: the default date is Argentina's.
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-09-06T02:00:00Z"));
    fireEvent.click(within(card1).getByRole("button", { name: /Agregar precio de referencia/i }));
    const priceForm = within(card1).getByRole("form", { name: /Nuevo precio de referencia/i });
    expect((within(priceForm).getByLabelText("Fecha") as HTMLInputElement).value).toBe("2026-09-05");
    vi.useRealTimers();
    fireEvent.change(within(priceForm).getByLabelText("Descripción"), { target: { value: "Gazebo 6×3" } });
    fireEvent.change(within(priceForm).getByLabelText("Monto ($)"), { target: { value: "12500" } });
    fireEvent.change(within(priceForm).getByLabelText("Fecha"), { target: { value: "2026-09-05" } });
    fireEvent.submit(priceForm);

    await waitFor(() => {
      expect(sentTo(sent, "POST /contacts/c-1/reference-prices")[0]?.body).toEqual({
        description: "Gazebo 6×3",
        amountCents: 1250000,
        date: "2026-09-05",
      });
    });

    // Delete Banda Ficticia via ConfirmDialog; Esc cancels first.
    const card2 = screen.getByTestId("contact-c-2");
    fireEvent.click(within(card2).getByRole("button", { name: "Borrar contacto" }));
    const dialog = await screen.findByRole("dialog");
    pressEscape(dialog);
    await settle();
    expect(sentTo(sent, "DELETE /contacts/c-2")).toHaveLength(0);

    fireEvent.click(within(card2).getByRole("button", { name: "Borrar contacto" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Borrar" }));
    await waitFor(() => expect(screen.queryByText("Banda Ficticia")).toBeNull());
  });

  test("officer manages Rubros on /contactos/rubros, handles heading_in_use, and protects otros", async () => {
    const createdHeading = { id: "h-new", name: "Fotografía", archived: false, reserved: false };
    const renamedHeading = { ...createdHeading, name: "Foto y video" };
    const archivedHeading = { ...renamedHeading, archived: true };

    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", roles: ["officer"], mfa: "satisfied" })),
      "GET /contacts/headings": [
        ok(sampleHeadings),
        ok([createdHeading, ...sampleHeadings]),
        ok([renamedHeading, ...sampleHeadings]),
        ok([archivedHeading, ...sampleHeadings]),
      ],
      "POST /contacts/headings": ok(createdHeading, 201),
      "PATCH /contacts/headings/h-new": ok(renamedHeading),
      "POST /contacts/headings/h-new/archive": ok(archivedHeading),
      "DELETE /contacts/headings/h-gaz": fail(409, "heading_in_use"),
    });

    window.history.replaceState(null, "", "/contactos/rubros");
    render(<App />);
    expect(await screen.findByText("otros")).toBeTruthy();

    // "otros" has no rename/archive/delete buttons.
    const otrosRow = screen.getByTestId("heading-h-otr");
    expect(within(otrosRow).queryByRole("button")).toBeNull();

    // Create heading.
    fireEvent.change(screen.getByLabelText("Nombre del rubro"), { target: { value: "Fotografía" } });
    fireEvent.submit(screen.getByRole("form", { name: "Nuevo rubro" }));
    expect(await screen.findByText("Fotografía")).toBeTruthy();

    // Rename heading (input receives focus immediately on entering edit mode).
    const newRow = screen.getByTestId("heading-h-new");
    fireEvent.click(within(newRow).getByRole("button", { name: "Renombrar" }));
    const renameInput = within(newRow).getByLabelText("Nuevo nombre");
    await waitFor(() => expect(document.activeElement).toBe(renameInput));
    fireEvent.change(renameInput, { target: { value: "Foto y video" } });
    fireEvent.click(within(newRow).getByRole("button", { name: "Guardar" }));
    expect(await screen.findByText("Foto y video")).toBeTruthy();

    // Archive heading via ConfirmDialog.
    fireEvent.click(within(screen.getByTestId("heading-h-new")).getByRole("button", { name: "Archivar" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Archivar" }));
    await waitFor(() => expect(sentTo(sent, "POST /contacts/headings/h-new/archive")).toHaveLength(1));

    // Deleting heading in use shows Spanish message and focuses status alert.
    fireEvent.click(within(screen.getByTestId("heading-h-gaz")).getByRole("button", { name: "Borrar" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Borrar" }));
    await screen.findByText(/Ese rubro tiene contactos asociados/i);
    await waitFor(() => {
      expect(document.activeElement?.classList.contains("form-status")).toBe(true);
    });
  });

  test("contacts grids use minmax so 360px layout never overflows", () => {
    expect(css).toContain(".contacts-filters");
    expect(css).toContain(".contacts-grid");
    expect(css).not.toMatch(/\.(contacts|heading)-[a-z-]+\s*\{[^}]*grid-template-columns:\s*1fr\b/);
  });
});
