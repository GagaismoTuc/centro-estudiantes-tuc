import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());

test("officer adds a cost using a copied reference price and sees the event totals", async () => {
  const budget = { id: "b1", name: "Week", startsOn: "2026-09-21", endsOn: "2026-09-25", archived: false };
  const totals = { costCents: 2500000, incomeCents: 0, resultCents: -2500000, breakEvenCents: null };
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /event-budgets": ok([budget]),
    "GET /event-budgets/b1": [ok({ ...budget, totals: { costCents: 0, incomeCents: 0, resultCents: 0, breakEvenCents: null }, days: [], lines: [] }), ok({ ...budget, totals, days: [], lines: [{ id: "l1", kind: "cost", description: "Gazebo", quantity: 1, unitAmountCents: 2500000 }] })],
    "GET /contacts/headings": ok([{ id: "h1", name: "gazebos", archived: false, reserved: false }]),
    "GET /contacts": ok([{ id: "c1", headingId: "h1", name: "Gazebos", referencePrices: [{ id: "p1", description: "3x3", amountCents: 2500000, date: "2026-09-01" }] }]),
    "POST /event-budgets/b1/lines": ok({ id: "l1", kind: "cost", description: "Gazebo", quantity: 1, unitAmountCents: 2500000 }, 201),
  });
  window.history.replaceState(null, "", "/presupuestos");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Week" }));
  fireEvent.change(await screen.findByLabelText("Contacto"), { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Precio de referencia"), { target: { value: "p1" } });
  expect((screen.getByLabelText("Precio unitario ($)") as HTMLInputElement).value).toBe("25000");
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Gazebo" } });
  fireEvent.change(screen.getByLabelText("Cantidad"), { target: { value: "1" } });
  fireEvent.click(screen.getByRole("button", { name: "Agregar l\u00ednea" }));
  await waitFor(() => expect(sent.some((r) => r.method === "POST" && r.path === "/event-budgets/b1/lines" && (r.body as { unitAmountCents: number }).unitAmountCents === 2500000)).toBe(true));
  await waitFor(() => expect(screen.getByText(/^Costo:/).textContent).toContain("25.000"));
});

test("R3 the budget detail shows its dates as dd/mm/aaaa", async () => {
  const budget = { id: "b1", name: "Week", startsOn: "2026-11-20", endsOn: "2026-11-21", archived: false };
  const zero = { costCents: 0, incomeCents: 0, resultCents: 0, breakEvenCents: null };
  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /event-budgets": ok([budget]),
    "GET /event-budgets/b1": ok({ ...budget, totals: zero, days: [{ day: "2026-11-20", totals: zero }], lines: [] }),
    "GET /contacts/headings": ok([]),
    "GET /contacts": ok([]),
  });
  window.history.replaceState(null, "", "/presupuestos");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Week" }));
  expect(await screen.findByText("20/11/2026 - 21/11/2026")).toBeTruthy();
  expect(screen.getByText(/^20\/11\/2026: costo/)).toBeTruthy();
  expect(screen.queryByText(/2026-11-2/)).toBeNull();
});

test("invalid budget form focuses the first invalid field and blocks students", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /event-budgets": ok([]),
    "GET /contacts/headings": ok([]),
    "GET /contacts": ok([]),
  });
  window.history.replaceState(null, "", "/presupuestos");
  const { unmount } = render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Crear presupuesto" }));
  const nameInput = screen.getByLabelText("Nombre");
  expect(nameInput.getAttribute("aria-invalid")).toBe("true");
  expect(document.activeElement).toBe(nameInput);
  expect(screen.getByText("Escrib\u00ed un nombre.")).toBeTruthy();
  unmount();

  mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "not_required" })),
  });
  render(<App />);
  expect((await screen.findByRole("alert")).textContent).toContain("No ten\u00e9s acceso");
});

test("#130 R1 R2 R3 Presupuestos accepts 1.234,56, rejects ambiguous amounts without posting, and shows a negative result as -$ 195.269,88", async () => {
  const budget = { id: "b1", name: "Week", startsOn: "2026-09-21", endsOn: "2026-09-25", archived: false };
  const totals = { costCents: 19526988, incomeCents: 0, resultCents: -19526988, breakEvenCents: null };
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /event-budgets": ok([budget]),
    "GET /event-budgets/b1": ok({ ...budget, totals, days: [], lines: [] }),
    "GET /contacts/headings": ok([{ id: "h1", name: "gazebos", archived: false, reserved: false }]),
    "GET /contacts": ok([]),
    "POST /event-budgets/b1/lines": ok({ id: "l1", kind: "cost", description: "Gazebo", quantity: 1, unitAmountCents: 123456 }, 201),
  });
  window.history.replaceState(null, "", "/presupuestos");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Week" }));
  expect((await screen.findByText(/^Costo:/)).textContent).toContain("resultado: -$ 195.269,88.");
  fireEvent.change(screen.getByLabelText("Descripción"), { target: { value: "Gazebo" } });
  fireEvent.change(screen.getByLabelText("Cantidad"), { target: { value: "1" } });
  const amount = screen.getByLabelText("Precio unitario ($)");
  for (const raw of ["1.2345", "12,345", "1.23.4", "1,234", "-5", "0", "abc", ""]) {
    fireEvent.change(amount, { target: { value: raw } });
    fireEvent.click(screen.getByRole("button", { name: "Agregar línea" }));
    await waitFor(() => expect(amount.getAttribute("aria-invalid")).toBe("true"));
    expect(document.getElementById(amount.getAttribute("aria-describedby") ?? "")?.textContent).toBe("Ingresá un monto positivo con hasta dos decimales.");
  }
  expect(sent.filter((r) => r.method === "POST")).toHaveLength(0);
  fireEvent.change(amount, { target: { value: "1.234,56" } });
  fireEvent.click(screen.getByRole("button", { name: "Agregar línea" }));
  await waitFor(() => expect(sent.filter((r) => r.method === "POST" && r.path === "/event-budgets/b1/lines")).toHaveLength(1));
  expect(sent.find((r) => r.method === "POST")?.body).toMatchObject({ unitAmountCents: 123456 });
});

async function submitBudget(startsOn: string, endsOn: string) {
  fireEvent.change(await screen.findByLabelText("Nombre"), { target: { value: "Semana" } });
  fireEvent.change(screen.getByLabelText("Inicio"), { target: { value: startsOn } });
  fireEvent.change(screen.getByLabelText("Fin"), { target: { value: endsOn } });
  fireEvent.click(screen.getByRole("button", { name: "Crear presupuesto" }));
}

test("R1 (#134) the date range error names its cause", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /event-budgets": ok([]),
    "GET /contacts/headings": ok([]),
    "GET /contacts": ok([]),
    "POST /event-budgets": fail(422, "invalid_range"),
  });
  window.history.replaceState(null, "", "/presupuestos");
  render(<App />);
  await submitBudget("2026-11-05", "2026-11-01");
  expect(await screen.findByText("La fecha de fin no puede ser anterior al inicio.")).toBeTruthy();
  await submitBudget("2026-11-01", "2026-12-10");
  expect(await screen.findByText("El evento puede durar hasta 31 días.")).toBeTruthy();
  expect(screen.queryByText("La fecha de fin no puede ser anterior al inicio.")).toBeNull();
  expect(screen.queryByText(/Revisá el rango de fechas/)).toBeNull();
  expect(sentTo(sent, "POST /event-budgets").map((r) => r.body)).toEqual([
    { name: "Semana", startsOn: "2026-11-05", endsOn: "2026-11-01" },
    { name: "Semana", startsOn: "2026-11-01", endsOn: "2026-12-10" },
  ]);
});

test("R10 (#134) a server error without its own message shows the generic one, never the raw code", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /event-budgets": ok([]),
    "GET /contacts/headings": ok([]),
    "GET /contacts": ok([]),
    "POST /event-budgets": fail(500, "internal"),
  });
  window.history.replaceState(null, "", "/presupuestos");
  render(<App />);
  await submitBudget("2026-11-01", "2026-11-05");
  expect(await screen.findByText("Algo salió mal de nuestro lado. Probá de nuevo en un rato.")).toBeTruthy();
  expect(document.body.textContent).not.toContain("internal");
});
