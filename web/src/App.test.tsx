import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import indexHtml from "../index.html?raw";
import themeScript from "../public/theme.js?raw";
import { App } from "./App.tsx";
import { guardLeave } from "./lib/router.tsx";
import { THEME_KEY } from "./lib/theme.ts";
import css from "./styles.css?raw";
import { emulateDialog, fail, me, mockApi, noContent, ok, pressEscape, renderStrict, sentTo, settle, type Reply } from "./test/api.ts";

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
    expect(window.location.search).toBe("");
    expect(await screen.findByRole("heading", { level: 1, name: "Ingresá" })).toBeTruthy();
  });

  test("signed-out access to /padron redirects to /ingresar?next=%2Fpadron", async () => {
    mockApi({});
    renderAt("/padron");
    await expectPath("/ingresar");
    expect(window.location.search).toBe("?next=%2Fpadron");
    expect(await screen.findByRole("heading", { level: 1, name: "Ingresá" })).toBeTruthy();
  });

  test("a session with pending two-factor is sent to /seguridad/verificar instead of Inicio", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "school_admin", mfa: "pending" })) });
    renderAt("/");
    await expectPath("/seguridad/verificar");
    expect(window.location.search).toBe("");
  });

  test("signing out calls the API and returns to /ingresar", async () => {
    const sent = mockApi({
      "GET /auth/me": [ok(me())],
      "POST /auth/logout": noContent(),
    });
    renderAt("/");
    await screen.findByRole("heading", { name: "¡Hola!" });

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
    expect(screen.getByRole("heading", { name: "¡Hola!" })).toBeTruthy();
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
    expect(within(nav).getByRole("link", { name: "Caja" }).getAttribute("href")).toBe("/caja");
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

describe("document referrer policy", () => {
  test("web/index.html sets strict-origin referrer policy", () => {
    const doc = new DOMParser().parseFromString(indexHtml, "text/html");
    const meta = doc.querySelector('meta[name="referrer"]');
    expect(meta?.getAttribute("content")).toBe("strict-origin");
  });
});

/** The paragraph whose whole text (across inline children such as <bdi>) is text. */
function byText(container: HTMLElement, text: string): HTMLElement {
  return within(container).getByText((_, el) => el?.tagName === "P" && el.textContent === text);
}

// Inicio's own <header> also maps to "banner" in jsdom, so the top bar is found by class.
const topbar = () => document.querySelector<HTMLElement>(".topbar") as HTMLElement;

describe("school name", () => {
  test("the sidebar and the top bar show the active school's name as isolated text", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })) });
    renderAt("/");
    const sidebar = await screen.findByRole("complementary", { name: "Barra lateral" });
    const name = within(sidebar).getByText("Escuela Demo Tucumán");
    expect(name.tagName).toBe("BDI");
    expect(within(sidebar).getByText("Centro de Estudiantes")).toBeTruthy();
    expect(within(topbar()).getByText("Escuela Demo Tucumán").tagName).toBe("BDI");
  });

  test("markup in the name is shown as text", async () => {
    mockApi({ "GET /auth/me": ok(me({ tenantName: '<img src=x onerror="alert(1)">' })) });
    renderAt("/");
    const sidebar = await screen.findByRole("complementary", { name: "Barra lateral" });
    expect(within(sidebar).getByText('<img src=x onerror="alert(1)">')).toBeTruthy();
    expect(document.querySelector("img")).toBeNull();
  });

  test("without a school the sidebar and the top bar show no name", async () => {
    mockApi({ "GET /auth/me": ok(me({ tenantId: null, tenantName: null, role: null })) });
    renderAt("/");
    const sidebar = await screen.findByRole("complementary", { name: "Barra lateral" });
    expect(within(sidebar).getAllByText("Sin escuela").length).toBeGreaterThan(0);
    expect(sidebar.querySelector("bdi")).toBeNull();
    expect(within(topbar()).getByText("Centro de Estudiantes")).toBeTruthy();
    expect(topbar().querySelector("bdi")).toBeNull();
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
    await within(main).findByRole("heading", { name: "¡Hola!" });
    expect(byText(main, "Escuela Demo Tucum\u00e1n \u00b7 Administrador de la escuela")).toBeTruthy();
    expect(within(main).getByRole("link", { name: /Padrón/ })).toBeTruthy();
    expect(within(main).getAllByText("Próximamente")).toHaveLength(1);

    const banner = within(main).getByRole("region", { name: "Verificá tu mail" });
    expect(banner.textContent).toContain("Cuando creaste la cuenta te enviamos un enlace a ana@escuela.edu.ar.");
    expect(banner.textContent).toContain("Si pedís otro, el enlace anterior deja de servir.");
    fireEvent.click(within(banner).getByRole("button", { name: "Reenviar" }));
    expect(
      await within(main).findByText("Te enviamos un enlace nuevo a ana@escuela.edu.ar. Vence en 48 horas."),
    ).toBeTruthy();
    expect(sentTo(sent, "POST /auth/email/verification")).toHaveLength(1);
  });

  test("the greeting never shows the email address", async () => {
    mockApi({ "GET /auth/me": ok(me()) });
    renderAt("/");
    const main = await screen.findByRole("main");
    const heading = await within(main).findByRole("heading", { level: 1 });
    expect(heading.textContent).toBe("¡Hola!");
  });

  test("a verified email shows no verification banner", async () => {
    mockApi({ "GET /auth/me": ok(me({ emailVerified: true })) });
    renderAt("/");
    await screen.findByRole("heading", { name: "¡Hola!" });
    expect(screen.queryByRole("button", { name: "Reenviar" })).toBeNull();
  });

  test("without a school the greeting says so and no school name is shown", async () => {
    mockApi({ "GET /auth/me": ok(me({ tenantId: null, tenantName: null, role: null })) });
    renderAt("/");
    const main = await screen.findByRole("main");
    await within(main).findByRole("heading", { name: "¡Hola!" });
    expect(byText(main, "Sin escuela")).toBeTruthy();
    expect(screen.queryByText(/Escuela Demo/)).toBeNull();
  });

  test("R4 (#134) Finanzas and Elecciones link to their screens for anyone in a school", async () => {
    mockApi({ "GET /auth/me": ok(me()) });
    renderAt("/");
    const main = await screen.findByRole("main");
    await within(main).findByRole("heading", { name: "¡Hola!" });
    expect(within(main).getByRole("link", { name: /Finanzas/ }).getAttribute("href")).toBe("/caja");
    expect(within(main).getByRole("link", { name: /Elecciones/ }).getAttribute("href")).toBe("/eleccion");
    const soon = within(main).getAllByText("Próximamente");
    expect(soon).toHaveLength(1);
    expect(soon[0]!.closest("li")?.textContent).toContain("Notas");
  });

  test("R4 (#134) without a school Finanzas and Elecciones are not offered", async () => {
    mockApi({ "GET /auth/me": ok(me({ tenantId: null, tenantName: null, role: null })) });
    renderAt("/");
    const main = await screen.findByRole("main");
    await within(main).findByRole("heading", { name: "¡Hola!" });
    expect(within(main).queryByRole("link", { name: /Finanzas/ })).toBeNull();
    expect(within(main).queryByRole("link", { name: /Elecciones/ })).toBeNull();
    expect(within(main).queryByText("Finanzas")).toBeNull();
  });

  test("an API outage on start shows a retry message instead of a blank page", async () => {
    mockApi({ "GET /auth/me": fail(503, "unavailable") });
    renderAt("/");
    expect(await screen.findByText("Algo salió mal de nuestro lado. Probá de nuevo en un rato.")).toBeTruthy();
  });
});

describe("T3 A6: leaving a guarded screen asks in the app, in Spanish", () => {
  test("a nav link opens the in-app dialog; Cancelar stays and «Salir igual» leaves", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })) });
    const nativeConfirm = vi.spyOn(window, "confirm");
    renderAt("/");
    const nav = await screen.findByRole("navigation", { name: "Principal" });
    const release = guardLeave("Los códigos generados no se van a volver a mostrar.");
    try {
      fireEvent.click(within(nav).getByRole("link", { name: "Padrón" }));
      const dialog = await screen.findByRole("dialog", { name: "¿Salir de esta pantalla?" });
      expect(dialog.textContent).toContain("Los códigos generados no se van a volver a mostrar.");
      fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
      await waitFor(() => expect(screen.queryByRole("dialog", { name: "¿Salir de esta pantalla?" })).toBeNull());
      expect(window.location.pathname).toBe("/");

      fireEvent.click(within(nav).getByRole("link", { name: "Padrón" }));
      const again = await screen.findByRole("dialog", { name: "¿Salir de esta pantalla?" });
      fireEvent.click(within(again).getByRole("button", { name: "Salir igual" }));
      await expectPath("/padron");
      expect(nativeConfirm).not.toHaveBeenCalled();
    } finally {
      release();
      nativeConfirm.mockRestore();
    }
  });

  test("signing out asks the same way before ending the session", async () => {
    const sent = mockApi({ "GET /auth/me": ok(me()), "POST /auth/logout": noContent() });
    renderAt("/");
    const sidebar = await screen.findByRole("complementary");
    const release = guardLeave("Los códigos generados no se van a volver a mostrar.");
    try {
      fireEvent.click(within(sidebar).getByRole("button", { name: "Cerrar sesión" }));
      const dialog = await screen.findByRole("dialog", { name: "¿Salir de esta pantalla?" });
      expect(sentTo(sent, "POST /auth/logout")).toHaveLength(0);
      fireEvent.click(within(dialog).getByRole("button", { name: "Salir igual" }));
      await expectPath("/ingresar");
      expect(sentTo(sent, "POST /auth/logout")).toHaveLength(1);
    } finally {
      release();
    }
  });
});

describe("A6 under StrictMode: the leave dialog stays until the user answers", () => {
  let restoreDialog: () => void;
  beforeEach(() => {
    restoreDialog = emulateDialog();
  });
  afterEach(() => restoreDialog());

  const admin = me({ role: "school_admin", mfa: "satisfied" });
  const member = { id: "m1", fullName: "Ana Gómez", documentNumber: "40111222", hasActiveCode: false, hasAccount: false };
  const leaveTitle = "¿Salir de esta pantalla?";

  /** Opens /padron (from Inicio, so Back has somewhere to go) and issues a code. */
  async function codesOnScreen(extra: Record<string, Reply> = {}) {
    const sent = mockApi({
      "GET /auth/me": ok(admin),
      "GET /roster": ok([member]),
      "POST /roster/m1/access-code": ok({ code: "AAAA-BBBB-CCCC", expiresAt: "2026-12-13T15:00:00Z" }, 201),
      ...extra,
    });
    window.history.replaceState(null, "", "/");
    renderStrict(<App />);
    const nav = await screen.findByRole("navigation", { name: "Principal" });
    fireEvent.click(within(nav).getByRole("link", { name: "Padrón" }));
    const table = await screen.findByRole("table", { name: "Alumnos del padrón" });
    fireEvent.click(within(table).getByRole("button", { name: "Generar código" }));
    await screen.findByRole("region", { name: "Códigos generados" });
    return sent;
  }

  const codesShown = () => screen.queryByRole("region", { name: "Códigos generados" }) !== null;

  test("a sidebar link: the dialog stays; Cancelar keeps the codes, «Salir igual» leaves", async () => {
    await codesOnScreen();
    const sidebar = screen.getByRole("complementary");
    fireEvent.click(within(sidebar).getByRole("link", { name: "Inicio" }));
    await settle();
    const dialog = screen.getByRole("dialog", { name: leaveTitle });
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
    await settle();
    expect(screen.queryByRole("dialog", { name: leaveTitle })).toBeNull();
    expect(window.location.pathname).toBe("/padron");
    expect(codesShown()).toBe(true);

    fireEvent.click(within(sidebar).getByRole("link", { name: "Inicio" }));
    await settle();
    fireEvent.click(within(screen.getByRole("dialog", { name: leaveTitle })).getByRole("button", { name: "Salir igual" }));
    await expectPath("/");
    expect(await screen.findByRole("heading", { level: 1, name: "¡Hola!" })).toBeTruthy();
  });

  test("Esc on the leave dialog cancels it", async () => {
    await codesOnScreen();
    fireEvent.click(within(screen.getByRole("complementary")).getByRole("link", { name: "Inicio" }));
    await settle();
    pressEscape(screen.getByRole("dialog", { name: leaveTitle }));
    await settle();
    expect(screen.queryByRole("dialog", { name: leaveTitle })).toBeNull();
    expect(window.location.pathname).toBe("/padron");
    expect(codesShown()).toBe(true);
  });

  test("a link in the phone menu: the dialog outlives the closed menu", async () => {
    await codesOnScreen();
    fireEvent.click(screen.getByRole("button", { name: "Abrir menú" }));
    const drawer = document.getElementById("menu-drawer") as HTMLElement;
    fireEvent.click(within(drawer).getByRole("link", { name: "Inicio" }));
    await settle();
    expect(drawer.hasAttribute("open")).toBe(false);
    const dialog = screen.getByRole("dialog", { name: leaveTitle });
    fireEvent.click(within(dialog).getByRole("button", { name: "Salir igual" }));
    await expectPath("/");
  });

  test("«Cerrar sesión»: the dialog stays and «Salir igual» signs out", async () => {
    const sent = await codesOnScreen({ "POST /auth/logout": noContent() });
    fireEvent.click(within(screen.getByRole("complementary")).getByRole("button", { name: "Cerrar sesión" }));
    await settle();
    const dialog = screen.getByRole("dialog", { name: leaveTitle });
    expect(sentTo(sent, "POST /auth/logout")).toHaveLength(0);
    fireEvent.click(within(dialog).getByRole("button", { name: "Salir igual" }));
    await expectPath("/ingresar");
    expect(sentTo(sent, "POST /auth/logout")).toHaveLength(1);
  });

  test("the browser's Back button asks too; Cancelar stays, «Salir igual» goes back", async () => {
    await codesOnScreen();
    window.history.back();
    await settle();
    const dialog = screen.getByRole("dialog", { name: leaveTitle });
    expect(window.location.pathname).toBe("/padron");
    expect(codesShown()).toBe(true);
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
    await settle();
    expect(window.location.pathname).toBe("/padron");
    expect(codesShown()).toBe(true);

    window.history.back();
    await settle();
    fireEvent.click(within(screen.getByRole("dialog", { name: leaveTitle })).getByRole("button", { name: "Salir igual" }));
    await expectPath("/");
    expect(await screen.findByRole("heading", { level: 1, name: "¡Hola!" })).toBeTruthy();
  });

  test("cancelling a Back-triggered leave dialog moves focus to the screen h1", async () => {
    await codesOnScreen();
    const h1 = screen.getByRole("heading", { level: 1, name: "Padrón" });

    window.history.back();
    await settle();
    const dialog = screen.getByRole("dialog", { name: leaveTitle });
    const cancel = within(dialog).getByRole("button", { name: "Cancelar" });
    cancel.focus();
    fireEvent.click(cancel);
    await settle();
    expect(document.activeElement).toBe(h1);

    window.history.back();
    await settle();
    const dialog2 = screen.getByRole("dialog", { name: leaveTitle });
    within(dialog2).getByRole("button", { name: "Cancelar" }).focus();
    pressEscape(dialog2);
    await settle();
    expect(document.activeElement).toBe(h1);
  });

  test("cancelling a link-triggered leave dialog does not move focus to the h1", async () => {
    await codesOnScreen();
    const h1 = screen.getByRole("heading", { level: 1, name: "Padrón" });
    const link = within(screen.getByRole("complementary")).getByRole("link", { name: "Inicio" });
    link.focus();
    fireEvent.click(link);
    await settle();
    fireEvent.click(within(screen.getByRole("dialog", { name: leaveTitle })).getByRole("button", { name: "Cancelar" }));
    await settle();
    expect(document.activeElement).not.toBe(h1);
  });

  test("Back with nothing to lose just goes back", async () => {
    mockApi({ "GET /auth/me": ok(admin), "GET /roster": ok([member]) });
    window.history.replaceState(null, "", "/");
    renderStrict(<App />);
    const nav = await screen.findByRole("navigation", { name: "Principal" });
    fireEvent.click(within(nav).getByRole("link", { name: "Padrón" }));
    await screen.findByRole("table", { name: "Alumnos del padrón" });
    window.history.back();
    await expectPath("/");
    expect(await screen.findByRole("heading", { level: 1, name: "¡Hola!" })).toBeTruthy();
    expect(screen.queryByRole("dialog", { name: leaveTitle })).toBeNull();
  });

  test("the leave guard is active as soon as generated codes appear in the DOM", async () => {
    const sent = mockApi({
      "GET /auth/me": ok(admin),
      "GET /roster": ok([member]),
      "POST /roster/m1/access-code": ok({ code: "AAAA-BBBB-CCCC", expiresAt: "2026-12-13T15:00:00Z" }, 201),
      "POST /auth/logout": noContent(),
    });
    window.history.replaceState(null, "", "/");
    renderStrict(<App />);
    const nav = await screen.findByRole("navigation", { name: "Principal" });
    fireEvent.click(within(nav).getByRole("link", { name: "Padrón" }));
    const table = await screen.findByRole("table", { name: "Alumnos del padrón" });

    // Resolve on the DOM-commit microtask, before any passive-effect task can run.
    const codesMounted = new Promise<void>((resolve) => {
      const observer = new MutationObserver(() => {
        if (screen.queryByRole("region", { name: "Códigos generados" })) {
          observer.disconnect();
          resolve();
        }
      });
      observer.observe(document.body, { childList: true, subtree: true });
    });

    fireEvent.click(within(table).getByRole("button", { name: "Generar código" }));
    await codesMounted;

    const unload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(unload);
    expect(unload.defaultPrevented).toBe(true);

    fireEvent.click(within(screen.getByRole("complementary")).getByRole("button", { name: "Cerrar sesión" }));
    await settle();
    expect(screen.getByRole("dialog", { name: leaveTitle })).toBeTruthy();
    expect(sentTo(sent, "POST /auth/logout")).toHaveLength(0);
  });
});

test("home and sidebar show all roles", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "student", roles: ["student", "school_admin"], mfa: "satisfied" })) });
  renderAt("/");
  await screen.findByRole("heading", { name: "\u00a1Hola!" });
  const sidebar = screen.getByRole("complementary");
  expect(within(sidebar).getAllByText("Alumno, Administrador de la escuela")).toHaveLength(2);
  expect(screen.getByText(/Alumno, Administrador de la escuela/, { selector: ".page-header__school" })).toBeTruthy();
});

test("home and sidebar sort roles regardless of API order", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "officer", roles: ["officer", "school_admin", "student"], mfa: "satisfied" })) });
  renderAt("/");
  await screen.findByRole("heading", { name: "\u00a1Hola!" });
  const sidebar = screen.getByRole("complementary");
  expect(within(sidebar).getAllByText("Alumno, Integrante de la comisi\u00f3n directiva, Administrador de la escuela")).toHaveLength(2);
  expect(
    screen.getByText(/Alumno, Integrante de la comisi\u00f3n directiva, Administrador de la escuela/, {
      selector: ".page-header__school",
    }),
  ).toBeTruthy();
});
