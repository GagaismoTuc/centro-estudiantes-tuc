import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import type { Role } from "./auth/session.tsx";
import { fail, me, mockApi, noContent, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());

const dueSummary = {
  id: "d1",
  name: "Cuota de noviembre",
  amountCents: 200000,
  dueDate: "2026-11-30",
  createdAt: "2026-11-16T10:00:00Z",
  owingCount: 2,
  totalCount: 2,
};

const dueDetail = {
  ...dueSummary,
  members: [
    { rosterMemberId: "r1", fullName: "Ana Ficticia", status: "owing" },
    { rosterMemberId: "r2", fullName: "Beto Ficticio", status: "owing" },
  ],
};

const myDue = {
  id: "d1",
  name: "Cuota de noviembre",
  amountCents: 200000,
  dueDate: "2026-11-30",
  status: "owing",
};

test("R10 student sees Mis cuotas without full list or Nueva cuota", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me()),
    "GET /finance/dues/mine": ok([myDue]),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  expect(await screen.findByRole("heading", { name: "Mis cuotas" })).toBeTruthy();
  expect(await screen.findByText(/Debés \$ 2\.000,00 — vence el 30\/11\/2026/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Nueva cuota" })).toBeNull();
  expect(sentTo(sent, "GET /finance/dues")).toHaveLength(0);
});

test("R10 officer sees Nueva cuota, list, detail, Mis cuotas, and prevents double submit", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" })),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([myDue]),
    "GET /finance/dues/d1": ok(dueDetail),
    "POST /finance/dues": ok(dueDetail, 201),
  });
  const handler = vi.mocked(fetch).getMockImplementation()!;
  let release!: () => void;
  const pending = new Promise<void>((resolve) => { release = resolve; });
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    if ((input as Request).method === "POST") await pending;
    return handler(input, init);
  });

  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  expect(await screen.findByRole("button", { name: "Nueva cuota" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Mis cuotas" })).toBeTruthy();
  expect(await screen.findByText(/Debés \$ 2\.000,00 — vence el 30\/11\/2026/)).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Nueva cuota" }));
  fireEvent.change(screen.getByLabelText("Nombre"), { target: { value: "Cuota de noviembre" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Vencimiento"), { target: { value: "2026-11-30" } });
  const save = screen.getByRole("button", { name: "Guardar" });
  fireEvent.click(save);
  expect((save as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(save);
  release();

  await waitFor(() => expect(sentTo(sent, "POST /finance/dues")).toHaveLength(1));
  expect(sentTo(sent, "POST /finance/dues")[0]?.body).toEqual({
    name: "Cuota de noviembre",
    amountCents: 200000,
    dueDate: "2026-11-30",
  });

  fireEvent.click(await screen.findByRole("button", { name: "Ver detalle de Cuota de noviembre" }));
  expect(await screen.findByText("Ana Ficticia")).toBeTruthy();
  expect(screen.getByText("Beto Ficticio")).toBeTruthy();
});

test("R3 and R10 officer edits and deletes a due using ConfirmDialog", async () => {
  const updated = { ...dueDetail, name: "Cuota diciembre", amountCents: 250000, dueDate: "2026-12-15" };
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([myDue]),
    "PATCH /finance/dues/d1": ok(updated),
    "DELETE /finance/dues/d1": noContent(),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);

  fireEvent.click(await screen.findByRole("button", { name: "Editar Cuota de noviembre" }));
  fireEvent.change(screen.getByLabelText("Nombre"), { target: { value: "Cuota diciembre" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2500" } });
  fireEvent.change(screen.getByLabelText("Vencimiento"), { target: { value: "2026-12-15" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar cambios" }));

  await waitFor(() => expect(sentTo(sent, "PATCH /finance/dues/d1")).toHaveLength(1));
  expect(sentTo(sent, "PATCH /finance/dues/d1")[0]?.body).toEqual({
    name: "Cuota diciembre",
    amountCents: 250000,
    dueDate: "2026-12-15",
  });

  fireEvent.click(screen.getByRole("button", { name: "Borrar Cuota de noviembre" }));
  const dialog = await screen.findByRole("dialog");
  fireEvent.click(within(dialog).getByRole("button", { name: "Confirmar borrado" }));
  await waitFor(() => expect(sentTo(sent, "DELETE /finance/dues/d1")).toHaveLength(1));
});

test("R4 school admin and veedor read dues list and detail without edit controls", async () => {
  for (const role of ["school_admin", "veedor"]) {
    mockApi({
      "GET /auth/me": ok(me({ role: role as Role, mfa: "satisfied" })),
      "GET /finance/dues": ok([dueSummary]),
      "GET /finance/dues/d1": ok(dueDetail),
    });
    window.history.replaceState(null, "", "/cuotas");
    const { unmount } = render(<App />);
    expect(await screen.findByText("Cuota de noviembre")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Nueva cuota" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Editar/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Borrar/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Ver detalle de Cuota de noviembre" }));
    expect(await screen.findByText("Ana Ficticia")).toBeTruthy();
    unmount();
  }
});

test("CS-9 and CS-10 accessible validation focuses invalid field and server errors move focus", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /finance/dues": ok([]),
    "GET /finance/dues/mine": ok([]),
    "POST /finance/dues": fail(422, "empty_roster"),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nueva cuota" }));
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));

  const nameInput = screen.getByLabelText("Nombre") as HTMLInputElement;
  expect(nameInput.getAttribute("aria-invalid")).toBe("true");
  expect(document.getElementById(nameInput.getAttribute("aria-describedby") ?? "")?.textContent).toContain("Escribí un nombre.");
  expect(document.activeElement).toBe(nameInput);

  fireEvent.change(nameInput, { target: { value: "Cuota de noviembre" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Vencimiento"), { target: { value: "2026-11-30" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));

  const statusMsg = await screen.findByText(/padrón está vacío/i);
  await waitFor(() => expect(document.activeElement).toBe(statusMsg.closest(".form-status")));
});

test("R3 due_frozen error and R6 mfa_required refresh on Dues", async () => {
  mockApi({
    "GET /auth/me": [ok(me({ role: "officer", mfa: "satisfied" })), ok(me({ role: "officer", mfa: "pending" }))],
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([myDue]),
    "PATCH /finance/dues/d1": fail(409, "due_frozen"),
    "DELETE /finance/dues/d1": fail(403, "mfa_required"),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Editar Cuota de noviembre" }));
  fireEvent.click(screen.getByRole("button", { name: "Guardar cambios" }));
  expect(await screen.findByText(/ya tiene pagos o exenciones/i)).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Borrar Cuota de noviembre" }));
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Confirmar borrado" }));
  await waitFor(() => expect(screen.getByRole("heading", { name: /Verificá/i })).toBeTruthy());
});

test("PRESERVE navigation includes Cuotas link and navigates to /cuotas", async () => {
  mockApi({
    "GET /auth/me": ok(me()),
    "GET /finance/dues/mine": ok([myDue]),
  });
  window.history.replaceState(null, "", "/");
  render(<App />);
  const nav = await screen.findByRole("navigation", { name: "Principal" });
  const link = within(nav).getByRole("link", { name: "Cuotas" });
  expect(link.getAttribute("href")).toBe("/cuotas");
  fireEvent.click(link);
  expect(await screen.findByRole("heading", { name: "Mis cuotas" })).toBeTruthy();
});
