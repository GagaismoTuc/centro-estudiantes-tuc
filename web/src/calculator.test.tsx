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
  test("calculating price per person and raffle fires no POST, PATCH, or DELETE requests", async () => {
    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    });
    renderAt("/calculadora");

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
  test("updates formatted results live in aria-live regions and exposes accessible error states", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })) });
    renderAt("/calculadora");

    const personSection = await screen.findByRole("region", { name: "Precio por persona" });
    const personLive = personSection.querySelector('[aria-live="polite"]');
    expect(personLive).not.toBeNull();

    const peopleInput = within(personSection).getByLabelText("Cantidad de personas");
    fireEvent.change(within(personSection).getByLabelText("Costo total ($)"), { target: { value: "100000" } });
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
});

describe("PRESERVE — App, AppShell, and Icon", () => {
  test("App preserves MFA and signed-out route guards on /calculadora", async () => {
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
