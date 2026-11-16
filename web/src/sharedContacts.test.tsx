import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { emulateDialog, fail, me, mockApi, ok, pressEscape, sentTo, settle } from "./test/api.ts";

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

    // Confirming share sends POST and updates badge to "Pendiente de aprobación".
    fireEvent.click(within(card1).getByRole("button", { name: "Compartir" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Compartir" }));
    expect(await within(card1).findByText("Pendiente de aprobación")).toBeTruthy();
    expect(within(card1).queryByRole("button", { name: "Compartir" })).toBeNull();
    expect(sentTo(sent, "POST /contacts/c-1/share")).toHaveLength(1);

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
});
