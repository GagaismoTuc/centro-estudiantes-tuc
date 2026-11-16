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

const reversedOriginal = { ...entry, reversalId: "e2", reversedBy: "Beto Ficticio", reversedOn: "2026-11-14" };
const reversal = { ...entry, id: "e2", direction: "expense", date: "2026-11-14", description: "Reversión: Venta de tortas", recordedBy: "Beto Ficticio", reversesId: "e1" };

test("R9 officers revert a movement only after confirming, and cancelling sends nothing", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads,
    "GET /finance/entries": [ok([entry]), ok([reversedOriginal, reversal])],
    "GET /finance/balance": [ok({ balanceCents: 200000 }), ok({ balanceCents: 0 })],
    "POST /finance/entries/e1/reversal": ok(reversal, 201),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: /^Revertir/ }));
  let dialog = screen.getByRole("dialog", { name: "Revertir movimiento" });
  expect(dialog.textContent).toContain("¿Revertís este movimiento? Se va a cargar un movimiento opuesto con fecha de hoy.");
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(sentTo(sent, "POST /finance/entries/e1/reversal")).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: /^Revertir/ }));
  dialog = screen.getByRole("dialog", { name: "Revertir movimiento" });
  fireEvent.click(within(dialog).getByRole("button", { name: "Revertir" }));
  expect(await screen.findByText("$ 0,00")).toBeTruthy();
  expect(sentTo(sent, "POST /finance/entries/e1/reversal")).toHaveLength(1);
  expect(screen.getByText("Revertido por Beto Ficticio el 14/11/2026")).toBeTruthy();
});

test("R9 Revertir is shown only to officers", async () => {
  mockApi({ "GET /auth/me": ok(me()), ...reads });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("Venta de tortas")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /^Revertir/ })).toBeNull();
});

test("R9 reversed movements and Reversals have no Revertir", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads, "GET /finance/entries": ok([reversedOriginal, reversal]) });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("Reversión: Venta de tortas")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Nuevo movimiento" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: /^Revertir/ })).toBeNull();
});

test("R1 a reversed original shows who reversed it and when", async () => {
  mockApi({ "GET /auth/me": ok(me()), ...reads, "GET /finance/entries": ok([reversedOriginal, reversal]) });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("Revertido por Beto Ficticio el 14/11/2026")).toBeTruthy();
  expect(screen.getAllByText(/^Revertido por/)).toHaveLength(1);
});

test("R9 a failed reversal moves focus to the message", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads, "POST /finance/entries/e1/reversal": fail(409, "already_reversed") });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: /^Revertir/ }));
  fireEvent.click(within(screen.getByRole("dialog", { name: "Revertir movimiento" })).getByRole("button", { name: "Revertir" }));
  const message = await screen.findByText(/ya fue revertido/);
  await waitFor(() => expect(document.activeElement?.contains(message)).toBe(true));
});
