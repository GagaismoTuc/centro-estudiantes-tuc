import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
