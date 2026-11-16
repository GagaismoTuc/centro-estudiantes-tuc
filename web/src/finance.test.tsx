import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());
const categories = [
  { id: "c1", name: "Ventas", direction: "income", active: true, reserved: false },
  { id: "c2", name: "Cuotas", direction: "income", active: true, reserved: true },
  { id: "c3", name: "Viajes", direction: "expense", active: true, reserved: false },
];
const entry = { id: "e1", categoryId: "c1", direction: "income", date: "2026-11-10", amountCents: 200000, description: "Venta de tortas", recordedBy: "Ana Ficticia", recordedAt: "2026-11-14T12:00:00Z" };
const reads = { "GET /finance/categories": ok(categories), "GET /finance/balance": ok({ balanceCents: 200000 }), "GET /finance/entries": ok([entry]) };

test("R15 students read the ledger and balance without an entry form", async () => {
  mockApi({ "GET /auth/me": ok(me()), ...reads });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("$ 2.000,00")).toBeTruthy();
  expect(screen.getByText(/Registrado por Ana Ficticia/)).toBeTruthy();
  expect(screen.getByText(/Ventas.*2026-11-10/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Nuevo movimiento" })).toBeNull();
});

test("R15 school admins read the ledger without an entry form", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })), ...reads });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("$ 2.000,00")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Nuevo movimiento" })).toBeNull();
});

test("R15 officers record movements and R13 blocks double submission", async () => {
  const sent = mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads, "POST /finance/entries": ok(entry, 201) });
  const handler = vi.mocked(fetch).getMockImplementation()!;
  let release!: () => void;
  const pending = new Promise<void>((resolve) => { release = resolve; });
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    if ((input as Request).method === "POST") await pending;
    return handler(input, init);
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-10" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Venta de tortas" } });
  const save = screen.getByRole("button", { name: "Guardar" }); fireEvent.click(save);
  expect((save as HTMLButtonElement).disabled).toBe(true); fireEvent.click(save); release();
  await waitFor(() => expect(sentTo(sent, "POST /finance/entries")).toHaveLength(1));
  expect(sentTo(sent, "POST /finance/entries")[0]?.body).toMatchObject({ categoryId: "c1", direction: "income", date: "2026-11-10", amountCents: 200000, description: "Venta de tortas" });
});

test("R15 mfa_required returns officers to second factor", async () => {
  mockApi({ "GET /auth/me": [ok(me({ role: "officer", mfa: "satisfied" })), ok(me({ role: "officer", mfa: "pending" }))], ...reads, "POST /finance/entries": fail(403, "mfa_required") });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-10" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Venta" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  await waitFor(() => expect(window.location.pathname).toBe("/seguridad/verificar"));
});

test("R2 and R7 the screen preserves full int64 cents without rounding", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads });
  const handler = vi.mocked(fetch).getMockImplementation()!;
  let sent = "";
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const req = input as Request;
    const path = new URL(req.url).pathname;
    if (req.method === "GET" && path === "/api/finance/balance") return new Response('{"balanceCents":9223372036854775807}', { status: 200, headers: { "Content-Type": "application/json" } });
    if (req.method === "POST" && path === "/api/finance/entries") { sent = await req.text(); return new Response(JSON.stringify(entry), { status: 201, headers: { "Content-Type": "application/json" } }); }
    return handler(input, init);
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("$ 92.233.720.368.547.758,07")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-10" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "92233720368547758,07" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Fixture" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  await waitFor(() => expect(sent).toContain('"amountCents":9223372036854775807'));
});

test("R15 invalid form fields are announced and focused", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  const category = screen.getByLabelText("Categor\u00eda");
  expect(category.getAttribute("aria-invalid")).toBe("true");
  expect(document.activeElement).toBe(category);
  fireEvent.change(category, { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "0" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  const amount = screen.getByLabelText("Monto en pesos");
  await waitFor(() => expect(amount.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(amount);
  expect(document.getElementById(amount.getAttribute("aria-describedby") ?? "")?.textContent).toContain("monto positivo");
});

test("R8 officers manage categories by direction and see only permitted actions", async () => {
  const sent = mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads,
    "POST /finance/categories": ok({ id: "c4", name: "Buzos", direction: "expense", active: true, reserved: false }, 201),
    "PATCH /finance/categories/c1": ok({ ...categories[0], name: "Kiosco" }),
    "POST /finance/categories/c1/archive": ok({ ...categories[0], active: false }),
    "DELETE /finance/categories/c3": new Response(null, { status: 204 }),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Categor\u00edas" }));
  expect(await screen.findByText("Reservada para los pagos de cuotas")).toBeTruthy();
  const reserved = screen.getByText("Reservada para los pagos de cuotas").closest("li")!;
  expect(reserved.querySelector("button")).toBeNull();
  const used = screen.getByText("Ventas", { selector: "strong" }).closest("li")!;
  const unused = screen.getByText("Viajes", { selector: "strong" }).closest("li")!;
  expect(within(used).getByRole("button", { name: "Archivar" })).toBeTruthy();
  expect(within(used).queryByRole("button", { name: "Borrar" })).toBeNull();
  expect(within(unused).getByRole("button", { name: "Borrar" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Agregar categor\u00eda" }));
  const name = screen.getByLabelText("Nombre de la categor\u00eda");
  await waitFor(() => expect(name.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(name);
  fireEvent.change(screen.getByLabelText("Nombre de la categor\u00eda"), { target: { value: "Buzos" } });
  fireEvent.change(screen.getByLabelText("Sentido"), { target: { value: "expense" } });
  fireEvent.click(screen.getByRole("button", { name: "Agregar categor\u00eda" }));
  await waitFor(() => expect(sentTo(sent, "POST /finance/categories")).toHaveLength(1));
  fireEvent.click(within(used).getByRole("button", { name: "Renombrar" }));
  fireEvent.change(screen.getByLabelText("Nuevo nombre"), { target: { value: "Kiosco" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar nombre" }));
  await waitFor(() => expect(sentTo(sent, "PATCH /finance/categories/c1")).toHaveLength(1));
  fireEvent.click(within(used).getByRole("button", { name: "Archivar" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirmar archivo" }));
  await waitFor(() => expect(sentTo(sent, "POST /finance/categories/c1/archive")).toHaveLength(1));
  fireEvent.click(within(unused).getByRole("button", { name: "Borrar" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirmar borrado" }));
  await waitFor(() => expect(sentTo(sent, "DELETE /finance/categories/c3")).toHaveLength(1));
});

test("R1 created expense appears in the movement form", async () => {
  const added = { id: "c4", name: "Buzos", direction: "expense", active: true, reserved: false };
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads,
    "GET /finance/categories": [ok(categories), ok([...categories, added])],
    "POST /finance/categories": ok(added, 201),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Categor\u00edas" }));
  await screen.findByText("Reservada para los pagos de cuotas");
  fireEvent.change(screen.getByLabelText("Nombre de la categor\u00eda"), { target: { value: "Buzos" } });
  fireEvent.change(screen.getByLabelText("Sentido"), { target: { value: "expense" } });
  fireEvent.click(screen.getByRole("button", { name: "Agregar categor\u00eda" }));
  await screen.findByText("Buzos", { selector: "strong" });
  fireEvent.click(screen.getByRole("button", { name: "Nuevo movimiento" }));
  fireEvent.change(screen.getByLabelText("Tipo"), { target: { value: "expense" } });
  expect(await screen.findByRole("option", { name: "Buzos" })).toBeTruthy();
});

test("R2 renaming changes the category shown on historical movements", async () => {
  const renamed = [{ ...categories[0]!, name: "Kiosco" }, ...categories.slice(1)];
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads,
    "GET /finance/categories": [ok(categories), ok(renamed)],
    "PATCH /finance/categories/c1": ok(renamed[0]),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Categor\u00edas" }));
  const used = (await screen.findByText("Ventas", { selector: "strong" })).closest("li")!;
  fireEvent.click(within(used).getByRole("button", { name: "Renombrar" }));
  fireEvent.change(screen.getByLabelText("Nuevo nombre"), { target: { value: "Kiosco" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar nombre" }));
  expect(await screen.findByText(/Kiosco.*2026-11-10/)).toBeTruthy();
});

test("R3 archived categories disappear from entry choices but remain on historical movements", async () => {
  const archived = [{ ...categories[0]!, active: false }, ...categories.slice(1)];
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads,
    "GET /finance/categories": [ok(categories), ok(archived)],
    "POST /finance/categories/c1/archive": ok(archived[0]),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Categor\u00edas" }));
  const used = (await screen.findByText("Ventas", { selector: "strong" })).closest("li")!;
  fireEvent.click(within(used).getByRole("button", { name: "Archivar" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirmar archivo" }));
  await screen.findByText("(Archivada)", { exact: false });
  expect(screen.getByText(/Ventas.*2026-11-10/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Nuevo movimiento" }));
  expect(screen.queryByRole("option", { name: "Ventas" })).toBeNull();
});
