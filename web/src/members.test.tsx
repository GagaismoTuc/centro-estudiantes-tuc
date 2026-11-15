import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
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
  expect(await screen.findByText("Administrador de la escuela")).toBeTruthy();
  expect(screen.getByText("Integrante de la comisi\u00f3n directiva \u2014 Tesorero")).toBeTruthy();
  expect(screen.getByText("sin cuenta")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Alumno del padr\u00f3n"), { target: { value: "m2" } });
  fireEvent.click(screen.getByRole("button", { name: "Asignar cargo" }));
  await waitFor(() => expect(sentTo(sent, "POST /members/m2/officer")).toHaveLength(1));
  expect(window.location.href).not.toContain("Beto");
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
  await waitFor(() => expect(sentTo(sent, "DELETE /members/m1/officer")).toHaveLength(1));
  await screen.findByText("Se quit\u00f3 el cargo.");
  expect(screen.queryByText("Ana Ficticia")).toBeNull();
});
