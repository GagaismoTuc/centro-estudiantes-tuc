import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { exactMoneyJSON } from "./api/money.ts";
import { App } from "./App.tsx";
import type { Role } from "./auth/session.tsx";
import css from "./styles.css?raw";
import { emulateDialog, me, mockApi, ok, pressEscape, sentTo } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());

const defaultSummary = { rosterCount: 480, balance: 15_000_000, owedOnDues: 31_600_000 };

function renderAt(url: string) {
  window.history.replaceState(null, "", url);
  return render(<App />);
}

describe("R4 — No guarda nada", () => {
  test("calculating price per person and raffle fires no POST, PATCH, or DELETE requests and resets on reload", async () => {
    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
      "GET /finance/calculator-summary": ok(defaultSummary),
    });
    const firstRender = renderAt("/calculadora");

    const personSection = await screen.findByRole("region", { name: "Precio por persona" });
    fireEvent.change(within(personSection).getByLabelText("Costo total ($)"), { target: { value: "180000" } });
    fireEvent.change(within(personSection).getByLabelText("Cantidad de personas"), { target: { value: "45" } });
    fireEvent.change(within(personSection).getByLabelText("Margen opcional (%)"), { target: { value: "10" } });

    const raffleSection = screen.getByRole("region", { name: "Rifa o venta" });
    fireEvent.change(within(raffleSection).getByLabelText("Costo total de premios o mercadería ($)"), {
      target: { value: "60000" },
    });
    fireEvent.change(within(raffleSection).getByLabelText("Precio por unidad ($)"), { target: { value: "1500" } });
    fireEvent.change(within(raffleSection).getByLabelText("Unidades disponibles"), { target: { value: "200" } });

    expect(within(personSection).getByText("$ 4.400,00")).toBeTruthy();
    expect(within(raffleSection).getByText("$ 240.000,00")).toBeTruthy();
    expect(sent.filter((request) => ["POST", "PATCH", "DELETE"].includes(request.method))).toHaveLength(0);

    // Simulating a page reload clears in-memory calculator inputs and results.
    firstRender.unmount();
    renderAt("/calculadora");
    const reloadedPerson = await screen.findByRole("region", { name: "Precio por persona" });
    expect((within(reloadedPerson).getByLabelText("Costo total ($)") as HTMLInputElement).value).toBe("");
    expect(within(reloadedPerson).queryByText("$ 4.400,00")).toBeNull();
  });
});

describe("R5 — Solo Officers", () => {
  test.each([
    { label: "student", overrides: { role: "student" as Role, mfa: "not_required" as const }, shown: false },
    { label: "school_admin", overrides: { role: "school_admin" as Role, mfa: "satisfied" as const }, shown: false },
    { label: "veedor", overrides: { role: "veedor" as unknown as Role, mfa: "satisfied" as const }, shown: false },
    { label: "officer", overrides: { role: "officer" as Role, mfa: "satisfied" as const }, shown: true },
    {
      label: "multi-role student+officer",
      overrides: { role: "student" as Role, roles: ["student", "officer"] as Role[], mfa: "satisfied" as const },
      shown: true,
    },
  ])("shows Calculadora menu entry only for officer ($label) and denies /calculadora to non-officers", async ({ overrides, shown }) => {
    const sent = mockApi({
      "GET /auth/me": ok(me(overrides)),
      "GET /finance/calculator-summary": ok(defaultSummary),
    });
    renderAt("/calculadora");

    const nav = await screen.findByRole("navigation", { name: "Principal" });
    expect(within(nav).queryByRole("link", { name: "Calculadora" }) !== null).toBe(shown);

    if (shown) {
      expect(await screen.findByRole("region", { name: "Precio por persona" })).toBeTruthy();
      expect(screen.queryByText("No tenés acceso a esta sección")).toBeNull();
      await waitFor(() => expect(sentTo(sent, "GET /finance/calculator-summary").length).toBeGreaterThanOrEqual(1));
    } else {
      expect(await screen.findByText("No tenés acceso a esta sección")).toBeTruthy();
      expect(screen.queryByRole("region", { name: "Precio por persona" })).toBeNull();
      expect(sentTo(sent, "GET /finance/calculator-summary")).toHaveLength(0);
    }
  });
});

describe("R6 — Pantalla accesible y en vivo", () => {
  test("updates formatted results live in aria-live regions and moves focus to first invalid field on submit", async () => {
    mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
      "GET /finance/calculator-summary": ok(defaultSummary),
    });
    renderAt("/calculadora");

    const personSection = await screen.findByRole("region", { name: "Precio por persona" });
    const personLive = personSection.querySelector('[aria-live="polite"]');
    expect(personLive).not.toBeNull();

    const costInput = within(personSection).getByLabelText("Costo total ($)");
    const peopleInput = within(personSection).getByLabelText("Cantidad de personas");
    fireEvent.submit(personSection.querySelector("form")!);
    expect(costInput.getAttribute("aria-invalid")).toBe("true");
    expect(document.activeElement).toBe(costInput);
    expect(personLive?.textContent).toContain("Ingresá el costo total");

    fireEvent.change(costInput, { target: { value: "100000" } });
    fireEvent.change(peopleInput, { target: { value: "0" } });
    expect(peopleInput.getAttribute("aria-invalid")).toBe("true");
    expect(document.getElementById(peopleInput.getAttribute("aria-describedby") ?? "")?.textContent).toContain(
      "Ingresá cuántas personas van",
    );

    fireEvent.change(peopleInput, { target: { value: "30" } });
    expect(peopleInput.getAttribute("aria-invalid")).toBeNull();
    expect(personLive?.textContent).toContain("$ 3.400,00");

    const raffleSection = screen.getByRole("region", { name: "Rifa o venta" });
    const raffleLive = raffleSection.querySelector('[aria-live="polite"]');
    expect(raffleLive).not.toBeNull();

    fireEvent.change(within(raffleSection).getByLabelText("Costo total de premios o mercadería ($)"), {
      target: { value: "400000" },
    });
    fireEvent.change(within(raffleSection).getByLabelText("Precio por unidad ($)"), { target: { value: "1500" } });
    fireEvent.change(within(raffleSection).getByLabelText("Unidades disponibles"), { target: { value: "200" } });
    expect(raffleLive?.textContent).toContain("Ni vendiendo todo cubrís el costo: perdés $ 100.000,00");

    const gridRule = css.match(/\.calc-grid\s*\{([^}]*)\}/);
    expect(gridRule).not.toBeNull();
    expect(gridRule![1]).toContain("minmax(");
  });

  test("announces the visible error for the field just edited in both calculators", async () => {
    mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
      "GET /finance/calculator-summary": ok(defaultSummary),
    });
    renderAt("/calculadora");

    const personSection = await screen.findByRole("region", { name: "Precio por persona" });
    const personLive = personSection.querySelector("[aria-live]");
    const marginInput = within(personSection).getByLabelText("Margen opcional (%)");

    // Empty earlier fields must not mask the edited field's error.
    fireEvent.change(marginInput, { target: { value: "-1" } });
    expect(personLive?.textContent).toContain("El margen no puede ser negativo");

    // Even when an earlier field already has a visible error, editing margin announces margin's error.
    fireEvent.blur(within(personSection).getByLabelText("Costo total ($)"));
    expect(personLive?.textContent).toContain("Ingresá el costo total");
    fireEvent.change(marginInput, { target: { value: "-2" } });
    expect(personLive?.textContent).toContain("El margen no puede ser negativo");

    // Fixing margin falls back to the remaining visible error.
    fireEvent.change(marginInput, { target: { value: "10" } });
    expect(personLive?.textContent).toContain("Ingresá el costo total");

    // Same class in RaffleCard: editing a later field announces that field's error, not an earlier untouched/invalid field.
    const raffleSection = screen.getByRole("region", { name: "Rifa o venta" });
    const raffleLive = raffleSection.querySelector("[aria-live]");
    const unitsInput = within(raffleSection).getByLabelText("Unidades disponibles");
    fireEvent.change(unitsInput, { target: { value: "0" } });
    expect(raffleLive?.textContent).toContain("Ingresá cuántas unidades hay disponibles");

    const unitPriceInput = within(raffleSection).getByLabelText("Precio por unidad ($)");
    fireEvent.change(unitPriceInput, { target: { value: "0" } });
    expect(raffleLive?.textContent).toContain("Ingresá el precio por unidad");
  });
});

describe("R2, R3, R4, R7 (#84) — Calculators from real data", () => {
  test("R2 and R7: calculates due amount with real roster and expected exemptions without writing", async () => {
    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
      "GET /finance/calculator-summary": ok(defaultSummary),
    });
    renderAt("/calculadora");

    const dueSection = await screen.findByRole("region", { name: "Monto de cuota" });
    expect(await within(dueSection).findByText(/480 alumnos/)).toBeTruthy();

    const goalInput = within(dueSection).getByLabelText("Meta de recaudación ($)");
    const exemptInput = within(dueSection).getByLabelText("Exentos previstos");
    expect((exemptInput as HTMLInputElement).value).toBe("0");

    // Goal $600.000 / 480 -> $ 1.300,00 (Sin redondeo: $ 1.250,00).
    fireEvent.change(goalInput, { target: { value: "600000" } });
    expect(within(dueSection).getByText("$ 1.300,00")).toBeTruthy();
    expect(within(dueSection).getByText("Sin redondeo: $ 1.250,00")).toBeTruthy();

    // With 30 expected exempt -> $600.000 / 450 = $ 1.333,33 -> $ 1.400,00.
    fireEvent.change(exemptInput, { target: { value: "30" } });
    expect(within(dueSection).getByText("$ 1.400,00")).toBeTruthy();
    expect(within(dueSection).getByText("Sin redondeo: $ 1.333,33")).toBeTruthy();

    // Negative: exempt >= roster shows error, marks aria-invalid, focuses on submit, hides button.
    fireEvent.change(exemptInput, { target: { value: "480" } });
    expect(exemptInput.getAttribute("aria-invalid")).toBe("true");
    expect(within(dueSection).getAllByText("Revisá la meta y la cantidad de alumnos").length).toBeGreaterThan(0);
    expect(within(dueSection).queryByRole("button", { name: "Crear Cuota con este monto" })).toBeNull();
    fireEvent.submit(dueSection.querySelector("form")!);
    expect(document.activeElement).toBe(exemptInput);

    // Negative: empty or 0 goal shows error and does not calculate.
    fireEvent.change(exemptInput, { target: { value: "0" } });
    fireEvent.change(goalInput, { target: { value: "0" } });
    expect(goalInput.getAttribute("aria-invalid")).toBe("true");
    expect(within(dueSection).getAllByText("Revisá la meta y la cantidad de alumnos").length).toBeGreaterThan(0);
    expect(within(dueSection).queryByRole("button", { name: "Crear Cuota con este monto" })).toBeNull();

    // R7: calculating fires no writes.
    expect(sent.filter((r) => ["POST", "PATCH", "DELETE"].includes(r.method))).toHaveLength(0);
  });

  test("R3: Crear Cuota con este monto opens prefilled #74 form, cancel does no POST, confirm posts due", async () => {
    const restoreDialog = emulateDialog();
    try {
      const createdDue = {
        id: "due-new",
        name: "Cuota de noviembre",
        amountCents: 130_000,
        dueDate: "2026-11-30",
        createdAt: "2026-11-19T12:00:00Z",
        owingCount: 480,
        totalCount: 480,
        members: [],
      };
      const sent = mockApi({
        "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
        "GET /finance/calculator-summary": [
          ok(defaultSummary),
          ok({ rosterCount: 480, balance: 15_000_000, owedOnDues: 94_000_000 }),
        ],
        "POST /finance/dues": ok(createdDue, 201),
      });
      renderAt("/calculadora");

      const dueSection = await screen.findByRole("region", { name: "Monto de cuota" });
      await within(dueSection).findByText(/480 alumnos/);
      fireEvent.change(within(dueSection).getByLabelText("Meta de recaudación ($)"), { target: { value: "600000" } });

      const openButton = within(dueSection).getByRole("button", { name: "Crear Cuota con este monto" });
      fireEvent.click(openButton);

      const dialog = screen.getByRole("dialog", { name: "Nueva cuota" });
      const nameInput = within(dialog).getByLabelText("Nombre") as HTMLInputElement;
      const amountInput = within(dialog).getByLabelText("Monto en pesos") as HTMLInputElement;
      const dateInput = within(dialog).getByLabelText("Vencimiento") as HTMLInputElement;

      expect(nameInput.value).toBe("");
      expect(amountInput.value).toBe("1300");
      expect(dateInput.value).toBe("");

      // Closing via Cancelar creates nothing and returns focus to the button.
      fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
      expect(screen.queryByRole("dialog", { name: "Nueva cuota" })).toBeNull();
      expect(sentTo(sent, "POST /finance/dues")).toHaveLength(0);
      expect(document.activeElement).toBe(openButton);

      // Reopen and close via Escape: still no POST.
      fireEvent.click(openButton);
      const reopened = screen.getByRole("dialog", { name: "Nueva cuota" });
      pressEscape(reopened);
      await waitFor(() => expect(screen.queryByRole("dialog", { name: "Nueva cuota" })).toBeNull());
      expect(sentTo(sent, "POST /finance/dues")).toHaveLength(0);

      // Reopen, submit invalid (empty name): #74 validation blocks POST and focuses Nombre.
      fireEvent.click(openButton);
      const activeDialog = screen.getByRole("dialog", { name: "Nueva cuota" });
      fireEvent.click(within(activeDialog).getByRole("button", { name: "Guardar" }));
      expect(within(activeDialog).getByText("Escribí un nombre.")).toBeTruthy();
      expect(document.activeElement).toBe(within(activeDialog).getByLabelText("Nombre"));
      expect(sentTo(sent, "POST /finance/dues")).toHaveLength(0);

      // Fill required fields and confirm: fires POST /finance/dues with prefilled amount.
      fireEvent.change(within(activeDialog).getByLabelText("Nombre"), { target: { value: "Cuota de noviembre" } });
      fireEvent.change(within(activeDialog).getByLabelText("Vencimiento"), { target: { value: "2026-11-30" } });
      fireEvent.click(within(activeDialog).getByRole("button", { name: "Guardar" }));

      await waitFor(() => expect(sentTo(sent, "POST /finance/dues")).toHaveLength(1));
      expect(sentTo(sent, "POST /finance/dues")[0]?.body).toEqual({
        name: "Cuota de noviembre",
        amountCents: 130_000,
        dueDate: "2026-11-30",
      });
      await waitFor(() => expect(screen.queryByRole("dialog", { name: "Nueva cuota" })).toBeNull());
      const statusText = await within(dueSection).findByText("Se creó la cuota.");
      expect(statusText).toBeTruthy();
      expect(document.activeElement).toBe(statusText.closest(".form-status"));
      // Summary reloads after creation so spendable reflects the newly created due.
      await waitFor(() => expect(sentTo(sent, "GET /finance/calculator-summary").length).toBeGreaterThanOrEqual(2));
    } finally {
      restoreDialog();
    }
  });

  test("R4 and R7: displays spendable balance + owed, planned cost shortfall, and negative balance without writing", async () => {
    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
      "GET /finance/calculator-summary": ok(defaultSummary),
    });
    const first = renderAt("/calculadora");

    const spendableSection = await screen.findByRole("region", { name: "Cuánto podemos gastar" });
    expect(
      await within(spendableSection).findByText("Hoy tenés $ 150.000,00; si cobrás lo que falta, $ 466.000,00"),
    ).toBeTruthy();

    const plannedInput = within(spendableSection).getByLabelText("Costo planeado opcional ($)");
    fireEvent.change(plannedInput, { target: { value: "500000" } });
    expect(within(spendableSection).getByText("Aunque cobres todo, te faltan $ 34.000,00")).toBeTruthy();

    // Invalid optional plannedCost keeps the real spendable summary visible, marks aria-invalid, and focuses on submit (CS-9).
    fireEvent.change(plannedInput, { target: { value: "-100" } });
    expect(within(spendableSection).getByText("Hoy tenés $ 150.000,00; si cobrás lo que falta, $ 466.000,00")).toBeTruthy();
    expect(plannedInput.getAttribute("aria-invalid")).toBe("true");
    expect(within(spendableSection).getAllByText("Ingresá un costo planeado válido").length).toBeGreaterThan(0);
    fireEvent.submit(spendableSection.querySelector("form")!);
    expect(document.activeElement).toBe(plannedInput);
    expect(sent.filter((r) => ["POST", "PATCH", "DELETE"].includes(r.method))).toHaveLength(0);

    // Negative balance renders in negative.
    first.unmount();
    mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
      "GET /finance/calculator-summary": ok({ rosterCount: 100, balance: -2_000_000, owedOnDues: 1_000_000 }),
    });
    const second = renderAt("/calculadora");
    const negSection = await screen.findByRole("region", { name: "Cuánto podemos gastar" });
    expect(
      await within(negSection).findByText("Hoy tenés -$ 20.000,00; si cobrás lo que falta, -$ 10.000,00"),
    ).toBeTruthy();

    // Values above Number.MAX_SAFE_INTEGER from the raw JSON response display without precision loss.
    second.unmount();
    mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })) });
    const baseFetch = vi.mocked(fetch).getMockImplementation()!;
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const req = input as Request;
      if (new URL(req.url).pathname === "/api/finance/calculator-summary") {
        return new Response('{"rosterCount":480,"balance":9007199254740993,"owedOnDues":200}', {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return baseFetch(input, init);
    });
    renderAt("/calculadora");
    const hugeSection = await screen.findByRole("region", { name: "Cuánto podemos gastar" });
    expect(
      await within(hugeSection).findByText(
        "Hoy tenés $ 90.071.992.547.409,93; si cobrás lo que falta, $ 90.071.992.547.411,93",
      ),
    ).toBeTruthy();
  });
});

describe("PRESERVE — App, AppShell, Icon, and exactMoneyJSON", () => {
  test("exactMoneyJSON preserves existing money keys and new calculator summary keys as exact strings", () => {
    const parsed = exactMoneyJSON(
      '{"rosterCount":480,"balance":-9007199254740993,"owedOnDues":9007199254740995,"balanceCents":123,"amountCents":456,"paidCents":789}',
    );
    expect(parsed).toEqual({
      rosterCount: 480,
      balance: "-9007199254740993",
      owedOnDues: "9007199254740995",
      balanceCents: "123",
      amountCents: "456",
      paidCents: "789",
    });
  });

  test("App preserves signed-out and MFA route guards on /calculadora", async () => {
    mockApi({});
    const signedOut = renderAt("/calculadora");
    await waitFor(() => expect(window.location.pathname).toBe("/ingresar"));
    expect(window.location.search).toBe("?next=%2Fcalculadora");
    signedOut.unmount();

    mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "pending" })) });
    renderAt("/calculadora");
    await waitFor(() => expect(window.location.pathname).toBe("/seguridad/verificar"));
  });

  test("AppShell navigates to /calculadora via the officer menu and keeps existing links", async () => {
    mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
      "GET /finance/calculator-summary": ok(defaultSummary),
    });
    renderAt("/");
    const nav = await screen.findByRole("navigation", { name: "Principal" });
    expect(within(nav).getByRole("link", { name: "Inicio" })).toBeTruthy();
    fireEvent.click(within(nav).getByRole("link", { name: "Calculadora" }));
    await waitFor(() => expect(window.location.pathname).toBe("/calculadora"));
    expect(await screen.findByRole("heading", { level: 1, name: "Calculadora" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Seguridad" })).toBeTruthy();
  });
});

test("#130 R1 R3 Calculadora reads 1.234,56 as pesos with thousands and shows a negative balance as -$ 195.269,88", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /finance/calculator-summary": ok({ rosterCount: 100, balance: -19_526_988, owedOnDues: 0 }),
  });
  renderAt("/calculadora");
  const personSection = await screen.findByRole("region", { name: "Precio por persona" });
  fireEvent.change(within(personSection).getByLabelText("Costo total ($)"), { target: { value: "1.234,56" } });
  fireEvent.change(within(personSection).getByLabelText("Cantidad de personas"), { target: { value: "1" } });
  expect(within(personSection).getByText("Sin redondeo: $ 1.234,56")).toBeTruthy();
  expect(within(personSection).getByText("$ 1.300,00")).toBeTruthy();
  const spendableSection = screen.getByRole("region", { name: "Cuánto podemos gastar" });
  expect(
    await within(spendableSection).findByText("Hoy tenés -$ 195.269,88; si cobrás lo que falta, -$ 195.269,88"),
  ).toBeTruthy();
  expect(sent.filter((r) => ["POST", "PATCH", "DELETE"].includes(r.method))).toHaveLength(0);
});
