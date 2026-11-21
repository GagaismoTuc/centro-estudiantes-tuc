import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { roleLabel, roleLabels } from "./auth/session.tsx";
import css from "./styles.css?raw";
import { fail, me, mockApi, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());

test("members lists roles and unclaimed officers and assigns a position", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": ok([
      { email: "admin@example.com", fullName: null, rosterMemberId: null, position: null, roles: ["school_admin"] },
      { email: null, fullName: "Ana Ficticia", rosterMemberId: "m1", position: "Tesorero", roles: ["student", "officer"] },
    ]),
    "GET /roster": ok([{ id: "m2", fullName: "Beto Ficticio", documentNumber: "30111223", hasActiveCode: false, hasAccount: false }]),
    "POST /members/m2/officer": ok(undefined, 201),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  expect((await screen.findAllByText("Administrador de la escuela")).length).toBeGreaterThan(0);
  expect(await screen.findByText("Integrante de la comisi\u00f3n directiva \u2014 Tesorero")).toBeTruthy();
  expect(screen.getByText("Alumno")).toBeTruthy();
  expect(screen.getByText("sin cuenta")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Alumno del padr\u00f3n"), { target: { value: "m2" } });
  fireEvent.click(screen.getByRole("button", { name: "Asignar cargo" }));
  await waitFor(() => expect(sentTo(sent, "POST /members/m2/officer")).toHaveLength(1));
  expect(window.location.href).not.toContain("Beto");
});

test("position errors identify and focus the position field", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": ok([]),
    "GET /roster": ok([{ id: "m2", fullName: "Beto Ficticio", documentNumber: "30111223", hasActiveCode: false, hasAccount: false }]),
    "POST /members/m2/officer": fail(409, "position_taken"),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  await screen.findByRole("option", { name: "Beto Ficticio" });
  fireEvent.change(screen.getByLabelText("Alumno del padr\u00f3n"), { target: { value: "m2" } });
  fireEvent.click(screen.getByRole("button", { name: "Asignar cargo" }));
  const field = screen.getByLabelText("Cargo");
  await waitFor(() => expect(field.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(field);
  expect(document.getElementById(field.getAttribute("aria-describedby") ?? "")?.textContent).toContain("Ese cargo ya est\u00e1 ocupado.");
});

test("members redirects to second factor after mfa_required", async () => {
  mockApi({
    "GET /auth/me": [ok(me({ role: "school_admin", mfa: "satisfied" })), ok(me({ role: "school_admin", mfa: "pending" }))],
    "GET /members": fail(403, "mfa_required"),
    "GET /roster": ok([]),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  await waitFor(() => expect(window.location.pathname).toBe("/seguridad/verificar"));
});

test("a school admin with another role retains access to members", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "student", roles: ["student", "school_admin"], mfa: "satisfied" })),
    "GET /members": ok([]),
    "GET /roster": ok([]),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  expect(await screen.findByRole("heading", { level: 1, name: "Miembros" })).toBeTruthy();
});

test("removing a position updates members", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": [ok([{ email: null, fullName: "Ana Ficticia", rosterMemberId: "m1", position: "Tesorero", roles: ["student", "officer"] }]), ok([])],
    "GET /roster": ok([]),
    "DELETE /members/m1/officer": ok(undefined, 204),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Quitar cargo de Ana Ficticia" }));
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Quitar cargo" }));
  await waitFor(() => expect(sentTo(sent, "DELETE /members/m1/officer")).toHaveLength(1));
  await screen.findByText("Se quit\u00f3 el cargo.");
  expect(screen.queryByText("Ana Ficticia")).toBeNull();
});

test("R1 Quitar cargo asks first: Cancelar changes nothing and returns focus, confirming focuses the message and clears old errors", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": [ok([{ email: null, fullName: "Ana Ficticia", rosterMemberId: "m1", position: "Tesorero", roles: ["student", "officer"] }]), ok([])],
    "GET /roster": ok([{ id: "m2", fullName: "Beto Ficticio", documentNumber: "30111223", hasActiveCode: false, hasAccount: false }]),
    "POST /members/m2/officer": fail(409, "position_taken"),
    "POST /members/invitations": fail(422, "invalid_email"),
    "DELETE /members/m1/officer": ok(undefined, 204),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  await screen.findByRole("option", { name: "Beto Ficticio" });
  fireEvent.change(screen.getByLabelText("Alumno del padrón"), { target: { value: "m2" } });
  fireEvent.click(screen.getByRole("button", { name: "Asignar cargo" }));
  await screen.findByText("Ese cargo ya está ocupado.");
  const email = screen.getByLabelText("Correo electrónico");
  fireEvent.change(email, { target: { value: "invalid@ejemplo.test" } });
  fireEvent.click(screen.getByRole("button", { name: "Invitar Veedor" }));
  await screen.findByText("Revisá el mail: no parece válido.");

  const remove = screen.getByRole("button", { name: "Quitar cargo de Ana Ficticia" });
  remove.focus();
  fireEvent.click(remove);
  let dialog = await screen.findByRole("dialog", { name: "Quitar cargo" });
  expect(dialog.textContent).toContain("Ana Ficticia");
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(remove));
  expect(sentTo(sent, "DELETE /members/m1/officer")).toHaveLength(0);
  expect(screen.getByText("Ana Ficticia")).toBeTruthy();
  expect(screen.getByText("Ese cargo ya está ocupado.")).toBeTruthy();

  fireEvent.click(remove);
  dialog = await screen.findByRole("dialog", { name: "Quitar cargo" });
  fireEvent.click(within(dialog).getByRole("button", { name: "Quitar cargo" }));
  const message = (await screen.findByText("Se quitó el cargo.")).closest("[aria-live]");
  await waitFor(() => expect(document.activeElement).toBe(message));
  expect(sentTo(sent, "DELETE /members/m1/officer")).toHaveLength(1);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByText("Ese cargo ya está ocupado.")).toBeNull();
  expect(screen.getByLabelText("Cargo").getAttribute("aria-invalid")).toBeNull();
  expect(screen.queryByText("Revisá el mail: no parece válido.")).toBeNull();
  expect(email.getAttribute("aria-invalid")).toBeNull();
});

test("member selects have constrained, touch-sized field styling", () => {
  const rule = css.match(/(?:^|\n)select\s*\{([^}]*)\}/);
  expect(rule).not.toBeNull();
  for (const declaration of ["inline-size: 100%", "min-inline-size: 0", "min-block-size: 2.875rem", "font-size: max(1rem, 1em)", "border:", "border-radius:"]) {
    expect(rule![1]).toContain(declaration);
  }
});

function memberRoleTexts(nameOrEmail: string): string[] {
  const heading = screen.getByRole("heading", { level: 2, name: "Miembros" });
  const list = heading.closest("section")!.querySelector(":scope > ul")!;
  const item = Array.from(list.children).find((el) => el.textContent?.includes(nameOrEmail));
  expect(item).toBeTruthy();
  const roleItems = within(item as HTMLElement).getByRole("list");
  return Array.from(roleItems.querySelectorAll("li")).map((li) => li.textContent ?? "");
}

test("members displays all three roles in Alumno, officer, Administrador order on Miembros", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": ok([
      {
        email: "carla@example.com",
        fullName: "Carla Ficticia",
        rosterMemberId: "m3",
        position: "Presidente",
        roles: ["school_admin", "officer", "student"],
      },
    ]),
    "GET /roster": ok([]),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  await screen.findByText("Carla Ficticia");
  expect(memberRoleTexts("Carla Ficticia")).toEqual([
    "Alumno",
    "Integrante de la comisi\u00f3n directiva \u2014 Presidente",
    "Administrador de la escuela",
  ]);
});

test("role order does not depend on the API order officer, school_admin, student", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "officer", roles: ["officer", "school_admin", "student"], mfa: "satisfied" })),
    "GET /members": ok([
      {
        email: "carla@example.com",
        fullName: "Carla Ficticia",
        rosterMemberId: "m3",
        position: "Presidente",
        roles: ["officer", "school_admin", "student"],
      },
    ]),
    "GET /roster": ok([]),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  await screen.findByText("Carla Ficticia");
  const sidebar = screen.getByRole("complementary");
  expect(within(sidebar).getAllByText("Alumno, Integrante de la comisi\u00f3n directiva, Administrador de la escuela")).toHaveLength(2);
  expect(memberRoleTexts("Carla Ficticia")).toEqual([
    "Alumno",
    "Integrante de la comisi\u00f3n directiva \u2014 Presidente",
    "Administrador de la escuela",
  ]);
});

test("single role and unclaimed officer without account keep their labels and order", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": ok([
      { email: "admin@example.com", fullName: null, rosterMemberId: null, position: null, roles: ["school_admin"] },
      { email: "diego@example.com", fullName: "Diego Ficticio", rosterMemberId: "m4", position: null, roles: ["student"] },
      { email: null, fullName: "Ana Ficticia", rosterMemberId: "m1", position: "Tesorero", roles: ["officer", "student"] },
    ]),
    "GET /roster": ok([]),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  await screen.findByText("Ana Ficticia");
  expect(memberRoleTexts("admin@example.com")).toEqual(["Administrador de la escuela"]);
  expect(memberRoleTexts("Diego Ficticio")).toEqual(["Alumno"]);
  expect(memberRoleTexts("Ana Ficticia")).toEqual([
    "Alumno",
    "Integrante de la comisi\u00f3n directiva \u2014 Tesorero",
  ]);
  expect(screen.getByText("sin cuenta")).toBeTruthy();
});

test("role labels match PR #65 strings in session.tsx", () => {
  expect(roleLabel).toEqual({
    school_admin: "Administrador de la escuela",
    officer: "Integrante de la comisi\u00f3n directiva",
    student: "Alumno",
    veedor: "Veedor",
  });
  expect(roleLabels(me({ role: "officer", roles: ["officer", "school_admin", "student"] }))).toBe(
    "Alumno, Integrante de la comisi\u00f3n directiva, Administrador de la escuela",
  );
  expect(roleLabels(me({ role: null }))).toBe("Sin escuela");
});

const boardElected = "La comisión directiva salió de la última elección; no se puede cambiar a mano";

test("assigning after an elected Board shows board_elected in voseo (S11)", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": ok([]),
    "GET /roster": ok([{ id: "m2", fullName: "Beto Ficticio", documentNumber: "30111223", hasActiveCode: false, hasAccount: false }]),
    "POST /members/m2/officer": fail(409, "board_elected"),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  await screen.findByRole("option", { name: "Beto Ficticio" });
  fireEvent.change(screen.getByLabelText("Alumno del padrón"), { target: { value: "m2" } });
  fireEvent.click(screen.getByRole("button", { name: "Asignar cargo" }));
  const status = await screen.findByText(boardElected);
  expect(status.closest("[role=alert]") ?? status.closest("[aria-live]")).toBeTruthy();
});

test("removing after an elected Board shows board_elected and keeps the officer (S11)", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": ok([{ email: null, fullName: "Ana Ficticia", rosterMemberId: "m1", position: "Presidente", roles: ["student", "officer"] }]),
    "GET /roster": ok([]),
    "DELETE /members/m1/officer": fail(409, "board_elected"),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Quitar cargo de Ana Ficticia" }));
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Quitar cargo" }));
  await screen.findByText(boardElected);
  expect(sentTo(sent, "DELETE /members/m1/officer")).toHaveLength(1);
  expect(memberRoleTexts("Ana Ficticia")).toEqual(["Alumno", "Integrante de la comisión directiva — Presidente"]);
});

test("officers with positions from an election show them and can lose them (S10)", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": ok([
      { email: "lu@example.com", fullName: "Lu Ficticia", rosterMemberId: "m5", position: "Presidenta", roles: ["student", "officer"] },
      { email: null, fullName: "Mo Inventado", rosterMemberId: "m6", position: "Vocal", roles: ["student", "officer"] },
    ]),
    "GET /roster": ok([]),
    "DELETE /members/m6/officer": ok(undefined, 204),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  await screen.findByText("Mo Inventado");
  expect(memberRoleTexts("Lu Ficticia")).toEqual(["Alumno", "Integrante de la comisión directiva — Presidenta"]);
  expect(memberRoleTexts("Mo Inventado")).toEqual(["Alumno", "Integrante de la comisión directiva — Vocal"]);
  fireEvent.click(screen.getByRole("button", { name: "Quitar cargo de Mo Inventado" }));
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Quitar cargo" }));
  await waitFor(() => expect(sentTo(sent, "DELETE /members/m6/officer")).toHaveLength(1));
});

test("R12: school admin invites a Veedor or school administrator by email", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": ok([]),
    "GET /roster": ok([]),
    "POST /members/invitations": [fail(422, "invalid_email"), ok(undefined, 202), ok(undefined, 202)],
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  const email = await screen.findByLabelText("Correo electr\u00f3nico");
  fireEvent.change(email, { target: { value: "invalid@ejemplo.test" } });
  fireEvent.click(screen.getByRole("button", { name: "Invitar Veedor" }));
  await waitFor(() => expect(email.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(email);
  expect(document.getElementById(email.getAttribute("aria-describedby") ?? "")?.getAttribute("aria-live")).toBe("polite");
  fireEvent.change(email, { target: { value: "veedora@ejemplo.test" } });
  fireEvent.click(screen.getByRole("button", { name: "Invitar Veedor" }));
  await waitFor(() => expect(sentTo(sent, "POST /members/invitations")).toHaveLength(2));
  expect(sentTo(sent, "POST /members/invitations")[1]!.body).toEqual({ email: "veedora@ejemplo.test", role: "veedor" });
  fireEvent.change(email, { target: { value: "admin@ejemplo.test" } });
  fireEvent.click(screen.getByRole("button", { name: "Invitar administrador" }));
  await waitFor(() => expect(sentTo(sent, "POST /members/invitations")).toHaveLength(3));
  expect(sentTo(sent, "POST /members/invitations")[2]!.body).toEqual({ email: "admin@ejemplo.test", role: "school_admin" });
  expect(await screen.findByText("Se gener\u00f3 la invitaci\u00f3n. Si no llega, pod\u00e9s reenviarla.")).toBeTruthy();
});

test("R3 (#134) inviting with an empty email asks for the person's email and sends nothing", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /members": ok([]),
    "GET /roster": ok([]),
  });
  window.history.replaceState(null, "", "/miembros");
  render(<App />);
  const email = await screen.findByLabelText("Correo electrónico");
  fireEvent.click(screen.getByRole("button", { name: "Invitar Veedor" }));
  await waitFor(() => expect(email.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(email);
  const describedBy = (email.getAttribute("aria-describedby") ?? "").split(" ");
  const message = describedBy.map((id) => document.getElementById(id)?.textContent ?? "").join(" ");
  expect(message).toContain("Escribí el mail de la persona.");
  expect(message).not.toContain("Escribí tu mail.");
  expect(sentTo(sent, "POST /members/invitations")).toHaveLength(0);
});
