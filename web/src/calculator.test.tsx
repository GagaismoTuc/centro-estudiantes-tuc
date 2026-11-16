import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import type { Role } from "./auth/session.tsx";
import css from "./styles.css?raw";
import { me, mockApi, ok } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());

function renderAt(url: string) {
  window.history.replaceState(null, "", url);
  return render(<App />);
}

describe("R4 — No guarda nada", () => {
  test("calculating price per person and raffle fires no POST, PATCH, or DELETE requests and resets on reload", async () => {
    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
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

    expect(within(personSection).getByText("$ 4.400")).toBeTruthy();
    expect(within(raffleSection).getByText("$ 240.000")).toBeTruthy();
    expect(sent.filter((request) => ["POST", "PATCH", "DELETE"].includes(request.method))).toHaveLength(0);

    // Simulating a page reload clears in-memory calculator inputs and results.
    firstRender.unmount();
    renderAt("/calculadora");
    const reloadedPerson = await screen.findByRole("region", { name: "Precio por persona" });
    expect((within(reloadedPerson).getByLabelText("Costo total ($)") as HTMLInputElement).value).toBe("");
    expect(within(reloadedPerson).queryByText("$ 4.400")).toBeNull();
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
    mockApi({ "GET /auth/me": ok(me(overrides)) });
    renderAt("/calculadora");

    const nav = await screen.findByRole("navigation", { name: "Principal" });
    expect(within(nav).queryByRole("link", { name: "Calculadora" }) !== null).toBe(shown);

    if (shown) {
      expect(await screen.findByRole("region", { name: "Precio por persona" })).toBeTruthy();
      expect(screen.queryByText("No tenés acceso a esta sección")).toBeNull();
    } else {
      expect(await screen.findByText("No tenés acceso a esta sección")).toBeTruthy();
      expect(screen.queryByRole("region", { name: "Precio por persona" })).toBeNull();
    }
  });
});

describe("R6 — Pantalla accesible y en vivo", () => {
  test("updates formatted results live in aria-live regions and moves focus to first invalid field on submit", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })) });
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
    expect(personLive?.textContent).toContain("$ 3.400");

    const raffleSection = screen.getByRole("region", { name: "Rifa o venta" });
    const raffleLive = raffleSection.querySelector('[aria-live="polite"]');
    expect(raffleLive).not.toBeNull();

    fireEvent.change(within(raffleSection).getByLabelText("Costo total de premios o mercadería ($)"), {
      target: { value: "400000" },
    });
    fireEvent.change(within(raffleSection).getByLabelText("Precio por unidad ($)"), { target: { value: "1500" } });
    fireEvent.change(within(raffleSection).getByLabelText("Unidades disponibles"), { target: { value: "200" } });
    expect(raffleLive?.textContent).toContain("Ni vendiendo todo cubrís el costo: perdés $ 100.000");

    const gridRule = css.match(/\.calc-grid\s*\{([^}]*)\}/);
    expect(gridRule).not.toBeNull();
    expect(gridRule![1]).toContain("minmax(");
  });

  test("announces the visible error for the field just edited in both calculators", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })) });
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

describe("PRESERVE — App, AppShell, and Icon", () => {
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
    mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })) });
    renderAt("/");
    const nav = await screen.findByRole("navigation", { name: "Principal" });
    expect(within(nav).getByRole("link", { name: "Inicio" })).toBeTruthy();
    fireEvent.click(within(nav).getByRole("link", { name: "Calculadora" }));
    await waitFor(() => expect(window.location.pathname).toBe("/calculadora"));
    expect(await screen.findByRole("heading", { level: 1, name: "Calculadora" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Seguridad" })).toBeTruthy();
  });
});
