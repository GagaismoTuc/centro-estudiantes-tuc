import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());

const categories = [
  { id: "c-alq", name: "Alquileres", direction: "expense", active: true, reserved: false },
  { id: "c-ven", name: "Ventas", direction: "income", active: true, reserved: false },
];

const activeBudget = { id: "b1", name: "Semana del Estudiante 2026", startsOn: "2026-09-21", endsOn: "2026-09-25", archived: false };
const archivedBudget = { id: "b2", name: "Pe\u00f1a 2025", startsOn: "2025-10-10", endsOn: "2025-10-11", archived: true };

const budgetDetail = {
  ...activeBudget,
  totals: { costCents: 30000000, incomeCents: 5000000, resultCents: -25000000, breakEvenCents: null },
  days: [],
  lines: [
    { id: "l1", kind: "cost", headingId: "h-chairs", description: "Alquiler mesas y sillas", quantity: 1, unitAmountCents: 10000000 },
    { id: "l2", kind: "cost", headingId: "h-gazebos", description: "Gazebos", quantity: 2, unitAmountCents: 10000000 },
    { id: "l3", kind: "sponsor", headingId: "h-artists", description: "Sponsor escenario", quantity: 1, unitAmountCents: 5000000 },
  ],
  byHeading: [
    {
      headingId: "h-artists",
      headingName: "artistas",
      budgetedExpenseCents: 0,
      actualExpenseCents: 0,
      expenseDifferenceCents: 0,
      budgetedIncomeCents: 5000000,
      actualIncomeCents: 6000000,
      incomeDifferenceCents: 1000000,
    },
    {
      headingId: "h-gazebos",
      headingName: "gazebos",
      budgetedExpenseCents: 20000000,
      actualExpenseCents: 0,
      expenseDifferenceCents: 20000000,
      budgetedIncomeCents: 0,
      actualIncomeCents: 0,
      incomeDifferenceCents: 0,
    },
    {
      headingId: "h-chairs",
      headingName: "mesas y sillas",
      budgetedExpenseCents: 10000000,
      actualExpenseCents: 11000000,
      expenseDifferenceCents: -1000000,
      budgetedIncomeCents: 0,
      actualIncomeCents: 0,
      incomeDifferenceCents: 0,
    },
    {
      headingId: "h-sound",
      headingName: "sonido",
      budgetedExpenseCents: 0,
      actualExpenseCents: 50000,
      expenseDifferenceCents: -50000,
      budgetedIncomeCents: 0,
      actualIncomeCents: 0,
      incomeDifferenceCents: 0,
    },
  ],
};

const linkedEntry = {
  id: "e1",
  categoryId: "c-alq",
  direction: "expense",
  date: "2026-11-14",
  amountCents: 11000000,
  description: "Alquiler de sillas",
  recordedBy: "Ana Ficticia",
  recordedAt: "2026-11-14T15:00:00Z",
  hasReceipt: false,
  eventBudgetId: "b1",
  eventBudgetName: "Semana del Estudiante 2026",
  headingId: "h-chairs",
  headingName: "mesas y sillas",
};

test("R1 and R5 officer links a new movement to an active event and heading, excluding archived budgets", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /finance/categories": ok(categories),
    "GET /finance/balance": [ok({ balanceCents: 0 }), ok({ balanceCents: -11000000 })],
    "GET /finance/entries": [ok([]), ok([linkedEntry])],
    "GET /event-budgets": ok([activeBudget, archivedBudget]),
    "GET /event-budgets/b1": ok(budgetDetail),
    "POST /finance/entries": ok(linkedEntry, 201),
  });

  window.history.replaceState(null, "", "/caja");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));

  const eventSelect = await screen.findByLabelText("Evento (opcional)");
  await waitFor(() => expect(within(eventSelect).getByRole("option", { name: "Semana del Estudiante 2026" })).toBeTruthy());
  expect(within(eventSelect).queryByRole("option", { name: /Pe\u00f1a 2025/ })).toBeNull();

  fireEvent.change(screen.getByLabelText("Tipo"), { target: { value: "expense" } });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c-alq" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-14" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "110000" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Alquiler de sillas" } });

  fireEvent.change(eventSelect, { target: { value: "b1" } });
  const headingSelect = await screen.findByLabelText("Rubro del evento");
  await waitFor(() => expect(within(headingSelect).getByRole("option", { name: "mesas y sillas" })).toBeTruthy());
  expect(within(headingSelect).getByRole("option", { name: "gazebos" })).toBeTruthy();
  // "sonido" has no line in budgetDetail.lines, so it must not be offered for linking (R2).
  expect(within(headingSelect).queryByRole("option", { name: "sonido" })).toBeNull();

  fireEvent.change(headingSelect, { target: { value: "h-chairs" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));

  await waitFor(() => expect(sentTo(sent, "POST /finance/entries")).toHaveLength(1));
  expect(sentTo(sent, "POST /finance/entries")[0]?.body).toMatchObject({
    categoryId: "c-alq",
    direction: "expense",
    date: "2026-11-14",
    amountCents: 11000000,
    description: "Alquiler de sillas",
    eventBudgetId: "b1",
    headingId: "h-chairs",
  });
  expect(await screen.findByText("Evento: Semana del Estudiante 2026 \u2014 mesas y sillas")).toBeTruthy();
});

test("R2 selecting an event without a heading blocks submit, marks aria-invalid, and focuses heading select", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /finance/categories": ok(categories),
    "GET /finance/balance": ok({ balanceCents: 0 }),
    "GET /finance/entries": ok([]),
    "GET /event-budgets": ok([activeBudget]),
    "GET /event-budgets/b1": ok(budgetDetail),
    "POST /finance/entries": fail(422, "heading_not_in_budget"),
  });

  window.history.replaceState(null, "", "/caja");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));

  const eventSelect = await screen.findByLabelText("Evento (opcional)");
  await waitFor(() => expect(within(eventSelect).getByRole("option", { name: "Semana del Estudiante 2026" })).toBeTruthy());

  fireEvent.change(screen.getByLabelText("Tipo"), { target: { value: "expense" } });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c-alq" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-14" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "110000" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Alquiler de sillas" } });
  fireEvent.change(eventSelect, { target: { value: "b1" } });

  const headingSelect = await screen.findByLabelText("Rubro del evento");
  await waitFor(() => expect(within(headingSelect).getByRole("option", { name: "mesas y sillas" })).toBeTruthy());

  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  expect(sentTo(sent, "POST /finance/entries")).toHaveLength(0);
  expect(headingSelect.getAttribute("aria-invalid")).toBe("true");
  expect(document.activeElement).toBe(headingSelect);
  const errId = headingSelect.getAttribute("aria-describedby") ?? "";
  expect(document.getElementById(errId)?.textContent).toContain("Eleg\u00ed un rubro");

  // Server rejection 422 heading_not_in_budget marks heading select invalid and focuses it.
  fireEvent.change(headingSelect, { target: { value: "h-chairs" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  await waitFor(() => expect(sentTo(sent, "POST /finance/entries")).toHaveLength(1));
  await waitFor(() => expect(headingSelect.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(headingSelect);
  expect(document.getElementById(headingSelect.getAttribute("aria-describedby") ?? "")?.textContent).toContain("no tiene l\u00edneas");
});

test("R3 and R4 event budget displays budgeted vs actual by heading with expenses and income separate and updates after reversal", async () => {
  const afterReversal = {
    ...budgetDetail,
    byHeading: budgetDetail.byHeading.map((row) =>
      row.headingId === "h-chairs"
        ? { ...row, actualExpenseCents: 0, expenseDifferenceCents: 10000000 }
        : row,
    ),
  };

  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /event-budgets": ok([activeBudget]),
    "GET /event-budgets/b1": [ok(budgetDetail), ok(afterReversal)],
    "GET /contacts/headings": ok([]),
    "GET /contacts": ok([]),
  });

  window.history.replaceState(null, "", "/presupuestos");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Semana del Estudiante 2026" }));

  const expensesRegion = await screen.findByRole("region", { name: "Gastos" });
  const chairsRow = within(expensesRegion).getByRole("row", { name: /mesas y sillas/ });
  expect(chairsRow.textContent).toContain("$ 100.000");
  expect(chairsRow.textContent).toContain("$ 110.000");
  expect(chairsRow.textContent).toContain("te pasaste $ 10.000");

  const gazebosRow = within(expensesRegion).getByRole("row", { name: /gazebos/ });
  expect(gazebosRow.textContent).toContain("$ 200.000");
  expect(gazebosRow.textContent).toContain("$ 0");
  expect(within(expensesRegion).queryByRole("row", { name: /artistas/ })).toBeNull();

  const incomeRegion = screen.getByRole("region", { name: "Ingresos" });
  const artistsRow = within(incomeRegion).getByRole("row", { name: /artistas/ });
  expect(artistsRow.textContent).toContain("$ 50.000");
  expect(artistsRow.textContent).toContain("$ 60.000");
  expect(artistsRow.textContent).toContain("$ 10.000");
  expect(within(incomeRegion).queryByRole("row", { name: /mesas y sillas/ })).toBeNull();

  // Reopen budget to load post-reversal state (R4): actual returns to $ 0.
  fireEvent.click(screen.getByRole("button", { name: "Semana del Estudiante 2026" }));
  await waitFor(() => {
    const updatedRow = within(screen.getByRole("region", { name: "Gastos" })).getByRole("row", { name: /mesas y sillas/ });
    expect(updatedRow.textContent).toContain("$ 0");
    expect(updatedRow.textContent).not.toContain("te pasaste");
  });
});

test("R5 archived budget still shows its comparison table and delete 409 budget_has_entries announces and focuses error", async () => {
  const archivedDetail = {
    ...archivedBudget,
    totals: { costCents: 10000000, incomeCents: 0, resultCents: -10000000, breakEvenCents: null },
    days: [],
    lines: [],
    byHeading: [
      {
        headingId: "h-chairs",
        headingName: "mesas y sillas",
        budgetedExpenseCents: 10000000,
        actualExpenseCents: 11000000,
        expenseDifferenceCents: -1000000,
        budgetedIncomeCents: 0,
        actualIncomeCents: 0,
        incomeDifferenceCents: 0,
      },
    ],
  };

  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /event-budgets": ok([activeBudget, archivedBudget]),
    "GET /event-budgets/b1": ok(budgetDetail),
    "GET /event-budgets/b2": ok(archivedDetail),
    "DELETE /event-budgets/b1": fail(409, "budget_has_entries"),
    "GET /contacts/headings": ok([]),
    "GET /contacts": ok([]),
  });

  window.history.replaceState(null, "", "/presupuestos");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Semana del Estudiante 2026" }));
  fireEvent.click((await screen.findAllByRole("button", { name: "Borrar" }))[0]!);
  const dialog = screen.getByRole("dialog", { name: "Confirmar borrado" });
  fireEvent.click(within(dialog).getByRole("button", { name: "Borrar" }));

  const errMsg = await screen.findByText(/movimientos ligados.*Archivalo/i);
  await waitFor(() => expect(document.activeElement?.contains(errMsg)).toBe(true));

  // Archived budget still renders its budgeted vs actual table.
  fireEvent.click(screen.getByRole("button", { name: "Pe\u00f1a 2025" }));
  const archivedExpenses = await screen.findByRole("region", { name: "Gastos" });
  expect(within(archivedExpenses).getByRole("row", { name: /mesas y sillas/ }).textContent).toContain("te pasaste $ 10.000");
});

test("R6 roles: students see event name in ledger and are blocked on budgets; school_admin and veedor read comparison table", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "not_required" })),
    "GET /finance/categories": ok(categories),
    "GET /finance/balance": ok({ balanceCents: -11000000 }),
    "GET /finance/entries": ok([linkedEntry]),
  });
  window.history.replaceState(null, "", "/caja");
  const studentCaja = render(<App />);
  expect(await screen.findByText("Evento: Semana del Estudiante 2026 \u2014 mesas y sillas")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Nuevo movimiento" })).toBeNull();
  studentCaja.unmount();

  for (const role of ["school_admin", "veedor"] as const) {
    mockApi({
      "GET /auth/me": ok(me({ role: role as never, roles: [role as never], mfa: "satisfied" })),
      "GET /event-budgets": ok([activeBudget]),
      "GET /event-budgets/b1": ok(budgetDetail),
    });
    window.history.replaceState(null, "", "/presupuestos");
    const view = render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Semana del Estudiante 2026" }));
    const expensesRegion = await screen.findByRole("region", { name: "Gastos" });
    expect(within(expensesRegion).getByRole("row", { name: /mesas y sillas/ }).textContent).toContain("te pasaste $ 10.000");
    expect(screen.queryByRole("button", { name: "Borrar" })).toBeNull();
    view.unmount();
  }
});

test("PRESERVE unlinked movement omits eventBudgetId and headingId and renders without event line", async () => {
  const unlinkedEntry = {
    id: "e2",
    categoryId: "c-ven",
    direction: "income",
    date: "2026-11-14",
    amountCents: 250000,
    description: "Rifa escolar",
    recordedBy: "Ana Ficticia",
    recordedAt: "2026-11-14T15:00:00Z",
    hasReceipt: false,
  };
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /finance/categories": ok(categories),
    "GET /finance/balance": [ok({ balanceCents: 0 }), ok({ balanceCents: 250000 })],
    "GET /finance/entries": [ok([]), ok([unlinkedEntry])],
    "GET /event-budgets": ok([activeBudget]),
    "POST /finance/entries": ok(unlinkedEntry, 201),
  });

  window.history.replaceState(null, "", "/caja");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c-ven" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-14" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2500" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Rifa escolar" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));

  await waitFor(() => expect(sentTo(sent, "POST /finance/entries")).toHaveLength(1));
  const body = sentTo(sent, "POST /finance/entries")[0]?.body as Record<string, unknown>;
  expect(body).not.toHaveProperty("eventBudgetId");
  expect(body).not.toHaveProperty("headingId");
  expect(await screen.findByText("Rifa escolar")).toBeTruthy();
  expect(screen.queryByText(/^Evento:/)).toBeNull();
});
