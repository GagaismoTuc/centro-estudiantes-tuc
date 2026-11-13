import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, test } from "vitest";
import themeScript from "../public/theme.js?raw";
import { App } from "./App.tsx";
import { THEME_KEY } from "./lib/theme.ts";
import css from "./styles.css?raw";
import { fail, me, mockApi, noContent, ok, sentTo } from "./test/api.ts";

function renderAt(url: string) {
  window.history.replaceState(null, "", url);
  return render(<App />);
}

async function expectPath(path: string) {
  await waitFor(() => expect(window.location.pathname).toBe(path));
}

describe("route guard", () => {
  test("signed-out access to an app route redirects to /ingresar", async () => {
    mockApi({});
    renderAt("/");
    await expectPath("/ingresar");
    expect(await screen.findByRole("heading", { level: 1, name: "Ingresá" })).toBeTruthy();
  });

  test("a session with pending two-factor is sent to /seguridad/verificar instead of Inicio", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "school_admin", mfa: "pending" })) });
    renderAt("/");
    await expectPath("/seguridad/verificar");
  });

  test("signing out calls the API and returns to /ingresar", async () => {
    const sent = mockApi({
      "GET /auth/me": [ok(me())],
      "POST /auth/logout": noContent(),
    });
    renderAt("/");
    await screen.findByRole("heading", { name: "Hola, ana@escuela.edu.ar" });

    const sidebar = screen.getByRole("complementary");
    fireEvent.click(within(sidebar).getByRole("button", { name: "Cerrar sesión" }));

    await expectPath("/ingresar");
    expect(sentTo(sent, "POST /auth/logout")).toHaveLength(1);
  });
});

describe("T6a advisories: sign-out", () => {
  test("a failed logout keeps the user signed in and says so", async () => {
    const sent = mockApi({ "GET /auth/me": ok(me()), "POST /auth/logout": fail(503, "unavailable") });
    renderAt("/");
    const sidebar = await screen.findByRole("complementary");
    fireEvent.click(within(sidebar).getByRole("button", { name: "Cerrar sesión" }));

    expect(
      await within(sidebar).findByText("No se cerró la sesión. Algo salió mal de nuestro lado. Probá de nuevo en un rato."),
    ).toBeTruthy();
    expect(window.location.pathname).toBe("/");
    expect(screen.getByRole("heading", { name: "Hola, ana@escuela.edu.ar" })).toBeTruthy();
    expect(sentTo(sent, "POST /auth/logout")).toHaveLength(1);
  });

  test("a logout answered with 401 counts as signed out", async () => {
    mockApi({ "GET /auth/me": [ok(me()), fail(401, "unauthenticated")], "POST /auth/logout": fail(401, "unauthenticated") });
    renderAt("/");
    const sidebar = await screen.findByRole("complementary");
    fireEvent.click(within(sidebar).getByRole("button", { name: "Cerrar sesión" }));
    await expectPath("/ingresar");
  });
});

describe("navigation", () => {
  test.each([
    ["student", false],
    ["officer", false],
    ["school_admin", true],
  ] as const)("Padrón for %s is shown: %s", async (role, shown) => {
    mockApi({ "GET /auth/me": ok(me({ role, mfa: role === "student" ? "not_required" : "satisfied" })) });
    renderAt("/");
    const nav = await screen.findByRole("navigation", { name: "Principal" });
    expect(within(nav).getByRole("link", { name: "Inicio" }).getAttribute("aria-current")).toBe("page");
    expect(within(nav).queryByRole("link", { name: "Padrón" }) !== null).toBe(shown);
    expect(within(nav).getByText("Finanzas")).toBeTruthy();
  });

  test("clicking a nav link changes the route without reloading", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })) });
    renderAt("/");
    const nav = await screen.findByRole("navigation", { name: "Principal" });
    fireEvent.click(within(nav).getByRole("link", { name: "Padrón" }));
    await expectPath("/padron");
    expect(await screen.findByRole("heading", { level: 1, name: "Padrón" })).toBeTruthy();
  });
});

describe("theme switch", () => {
  test("persists the choice and sets data-theme and the color-scheme meta", async () => {
    mockApi({ "GET /auth/me": ok(me()) });
    renderAt("/");
    const sidebar = await screen.findByRole("complementary");
    const meta = document.querySelector('meta[name="color-scheme"]');

    fireEvent.click(within(sidebar).getByRole("radio", { name: "Oscuro" }));
    expect(localStorage.getItem("color-scheme")).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(meta?.getAttribute("content")).toBe("dark");

    fireEvent.click(within(sidebar).getByRole("radio", { name: /^Sistema/ }));
    expect(localStorage.getItem("color-scheme")).toBeNull();
    expect(document.documentElement.dataset.theme).toBe("system");
    expect(meta?.getAttribute("content")).toBe("light dark");
  });
});

describe("T6a advisories: theme", () => {
  test("the sidebar and drawer switches share one theme store", async () => {
    mockApi({ "GET /auth/me": ok(me()) });
    renderAt("/");
    const sidebar = await screen.findByRole("complementary");
    fireEvent.click(screen.getByRole("button", { name: "Abrir menú" }));
    // jsdom has no showModal(), so the drawer's <dialog> never gets `open`.
    const drawer = document.getElementById("menu-drawer") as HTMLElement;
    fireEvent.click(within(drawer).getByRole("radio", { name: "Oscuro", hidden: true }));
    expect((within(sidebar).getByRole("radio", { name: "Oscuro" }) as HTMLInputElement).checked).toBe(true);
    expect(localStorage.getItem(THEME_KEY)).toBe("dark");
  });

  test("public/theme.js reads the same storage key as the app", () => {
    expect(themeScript).toContain(`localStorage.getItem("${THEME_KEY}")`);
  });

  test("each color token is declared once (light-dark), never duplicated for dark mode", () => {
    const declared = [...css.matchAll(/^\s*(--[a-z0-9-]+):/gm)].map((m) => m[1]);
    for (const token of ["--page", "--surface", "--text", "--muted", "--primary", "--accent", "--danger", "--ok", "--shadow"]) {
      expect(declared.filter((d) => d === token), token).toHaveLength(1);
    }
  });
});

describe("Inicio", () => {
  test("shows role, modules and, for an unverified email, sends a verification link", async () => {
    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied", emailVerified: false })),
      "POST /auth/email/verification": noContent(202),
    });
    renderAt("/");
    const main = await screen.findByRole("main");
    await within(main).findByRole("heading", { name: "Hola, ana@escuela.edu.ar" });
    expect(within(main).getByText("Autoridad escolar")).toBeTruthy();
    expect(within(main).getByRole("link", { name: /Padrón/ })).toBeTruthy();
    expect(within(main).getAllByText("Próximamente")).toHaveLength(3);

    fireEvent.click(within(main).getByRole("button", { name: "Enviar enlace de verificación" }));
    expect(await within(main).findByText("Te enviamos un enlace a ana@escuela.edu.ar. Vence en 48 horas.")).toBeTruthy();
    expect(sentTo(sent, "POST /auth/email/verification")).toHaveLength(1);
  });

  test("a verified email shows no verification banner", async () => {
    mockApi({ "GET /auth/me": ok(me({ emailVerified: true })) });
    renderAt("/");
    await screen.findByRole("heading", { name: "Hola, ana@escuela.edu.ar" });
    expect(screen.queryByRole("button", { name: "Enviar enlace de verificación" })).toBeNull();
  });

  test("an API outage on start shows a retry message instead of a blank page", async () => {
    mockApi({ "GET /auth/me": fail(503, "unavailable") });
    renderAt("/");
    expect(await screen.findByText("Algo salió mal de nuestro lado. Probá de nuevo en un rato.")).toBeTruthy();
  });
});
