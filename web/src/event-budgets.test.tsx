import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, ok } from "./test/api.ts";

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

test("a line that would overflow the budget totals marks the price and moves focus to it", async () => {
  const budget = { id: "b1", name: "Week", startsOn: "2026-09-21", endsOn: "2026-09-25", archived: false };
  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /event-budgets": ok([budget]),
    "GET /event-budgets/b1": ok({ ...budget, totals: { costCents: 0, incomeCents: 0, resultCents: 0, breakEvenCents: null }, days: [], lines: [] }),
    "GET /contacts/headings": ok([{ id: "h1", name: "gazebos", archived: false, reserved: false }]),
    "GET /contacts": ok([]),
    "POST /event-budgets/b1/lines": fail(422, "invalid_amount"),
  });
  window.history.replaceState(null, "", "/presupuestos");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Week" }));
  fireEvent.change(await screen.findByLabelText("Descripción"), { target: { value: "Huge" } });
  fireEvent.change(screen.getByLabelText("Cantidad"), { target: { value: "99999999" } });
  const price = screen.getByLabelText("Precio unitario ($)") as HTMLInputElement;
  fireEvent.change(price, { target: { value: "99999999999" } });
  fireEvent.click(screen.getByRole("button", { name: "Agregar línea" }));
  await waitFor(() => expect(document.activeElement).toBe(price));
  expect(price.getAttribute("aria-invalid")).toBe("true");
  const describedBy = (price.getAttribute("aria-describedby") ?? "").split(" ").map((id) => document.getElementById(id)?.textContent ?? "").join(" ");
  expect(describedBy).toContain("El total del presupuesto sería demasiado grande.");
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
