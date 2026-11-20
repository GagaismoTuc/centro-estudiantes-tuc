import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { test, expect } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, noContent, ok, renderStrict, sentTo } from "./test/api.ts";

function renderAt(url: string) {
  window.history.replaceState(null, "", url);
  return render(<App />);
}

test("invited new user creates a verified account with the fragment token", async () => {
  const sent = mockApi({
    "GET /auth/me": [fail(401, "unauthenticated"), ok(me({ role: "veedor", mfa: "enrollment_required" }))],
    "POST /auth/invitations/accept": ok({}, 201),
    "POST /auth/mfa/enrollment": ok({ secret: "JBSWY3DPEHPK3PXP", otpauthUri: "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP" }),
  });
  renderAt("/invitacion#token=invitation-new");
  const password = await screen.findByLabelText("Eleg\u00ed tu contrase\u00f1a");
  expect(window.location.hash).toBe("");
  fireEvent.change(password, { target: { value: "una-clave-segura" } });
  fireEvent.click(screen.getByRole("button", { name: "Aceptar invitaci\u00f3n" }));
  await waitFor(() => expect(sentTo(sent, "POST /auth/invitations/accept")[0]?.body).toEqual({ token: "invitation-new", password: "una-clave-segura" }));
  await waitFor(() => expect(window.location.pathname).toBe("/seguridad/activar"));
});

test("existing invited user signs in and verifies MFA before accepting without a new password", async () => {
  const sent = mockApi({
    "GET /auth/me": [fail(401, "unauthenticated"), ok(me({ role: "student", mfa: "pending" })), ok(me({ role: "student", mfa: "satisfied" })), ok(me({ role: "veedor", mfa: "satisfied" }))],
    "POST /auth/login": noContent(),
    "POST /auth/mfa/verify": noContent(),
    "POST /auth/invitations/accept": ok({}, 200),
  });
  renderAt("/invitacion#token=invitation-existing");
  fireEvent.click(await screen.findByRole("link", { name: /ya ten\u00e9s cuenta/i }));
  fireEvent.change(await screen.findByLabelText("Mail"), { target: { value: "persona@example.test" } });
  fireEvent.change(screen.getByLabelText("Contrase\u00f1a"), { target: { value: "clave-existente" } });
  fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));
  fireEvent.change(await screen.findByLabelText("C\u00f3digo de 6 d\u00edgitos"), { target: { value: "123456" } });
  fireEvent.click(screen.getByRole("button", { name: "Verificar" }));
  fireEvent.click(await screen.findByRole("button", { name: "Aceptar invitaci\u00f3n" }));
  await waitFor(() => expect(sentTo(sent, "POST /auth/invitations/accept")[0]?.body).toEqual({ token: "invitation-existing" }));
});

test("student invited to privileged role can complete MFA despite not_required session state", async () => {
  const sent = mockApi({
    "GET /auth/me": [ok(me({ role: "student", mfa: "not_required" })), ok(me({ role: "student", mfa: "not_required" })), ok(me({ role: "veedor", mfa: "satisfied" }))],
    "POST /auth/invitations/accept": [fail(403, "mfa_required"), ok({}, 200)],
    "POST /auth/mfa/verify": [fail(422, "invalid_code"), noContent()],
  });
  renderAt("/invitacion#token=student-invite");
  fireEvent.click(await screen.findByRole("button", { name: "Aceptar invitaci\u00f3n" }));
  fireEvent.click(await screen.findByRole("button", { name: "Ya tengo verificaci\u00f3n en dos pasos" }));
  fireEvent.change(await screen.findByLabelText("C\u00f3digo de 6 d\u00edgitos"), { target: { value: "123456" } });
  fireEvent.click(screen.getByRole("button", { name: "Verificar" }));
  await screen.findByText(/c\u00f3digo no es correcto/i);
  expect(sentTo(sent, "POST /auth/invitations/accept")).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Verificar" }));
  fireEvent.click(await screen.findByRole("button", { name: "Aceptar invitaci\u00f3n" }));
  await waitFor(() => expect(sentTo(sent, "POST /auth/invitations/accept")).toHaveLength(2));
});

test("existing student can enroll MFA and return to the invitation", async () => {
  const sent = mockApi({
    "GET /auth/me": [ok(me({ role: "student", mfa: "not_required" })), ok(me({ role: "student", mfa: "not_required" })), ok(me({ role: "veedor", mfa: "satisfied" }))],
    "POST /auth/invitations/accept": [fail(403, "mfa_required"), ok({}, 200)],
    "POST /auth/mfa/enrollment": ok({ secret: "JBSWY3DPEHPK3PXP", otpauthUri: "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP" }),
    "POST /auth/mfa/enrollment/confirm": ok({ recoveryCodes: ["AAAAA-BBBBB"] }),
  });
  renderAt("/invitacion#token=student-enroll");
  fireEvent.click(await screen.findByRole("button", { name: "Aceptar invitaci\u00f3n" }));
  fireEvent.click(await screen.findByRole("button", { name: "Activar verificaci\u00f3n en dos pasos" }));
  fireEvent.change(await screen.findByLabelText("C\u00f3digo de 6 d\u00edgitos"), { target: { value: "123456" } });
  fireEvent.click(screen.getByRole("button", { name: "Activar" }));
  fireEvent.click(await screen.findByLabelText("Ya los guard\u00e9 en un lugar seguro"));
  fireEvent.click(screen.getByRole("button", { name: "Continuar" }));
  fireEvent.click(await screen.findByRole("button", { name: "Aceptar invitaci\u00f3n" }));
  await waitFor(() => expect(sentTo(sent, "POST /auth/invitations/accept")).toHaveLength(2));
});

test("invitation fragment survives StrictMode and never reaches a request URL", async () => {
  const sent = mockApi({ "GET /auth/me": fail(401, "unauthenticated"), "POST /auth/invitations/accept": fail(422, "invalid_token") });
  window.history.replaceState(null, "", "/invitacion#token=strict-invite");
  renderStrict(<App />);
  fireEvent.change(await screen.findByLabelText("Eleg\u00ed tu contrase\u00f1a"), { target: { value: "una-clave-segura" } });
  fireEvent.click(screen.getByRole("button", { name: "Aceptar invitaci\u00f3n" }));
  await waitFor(() => expect(sentTo(sent, "POST /auth/invitations/accept")[0]?.body).toEqual({ token: "strict-invite", password: "una-clave-segura" }));
  expect(sent.every((request) => !request.href.includes("strict-invite"))).toBe(true);
});

test("wrong password leaves the invitation unspent and permits retry", async () => {
  const sent = mockApi({
    "GET /auth/me": fail(401, "unauthenticated"),
    "POST /auth/login": fail(401, "invalid_credentials"),
  });
  renderAt("/invitacion#token=wrong-password-invite");
  fireEvent.click(await screen.findByRole("link", { name: /ya ten\u00e9s cuenta/i }));
  fireEvent.change(await screen.findByLabelText("Mail"), { target: { value: "persona@example.test" } });
  fireEvent.change(screen.getByLabelText("Contrase\u00f1a"), { target: { value: "contrase\u00f1a-equivocada" } });
  fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));
  await waitFor(() => expect(sentTo(sent, "POST /auth/login")).toHaveLength(1));
  expect(sentTo(sent, "POST /auth/invitations/accept")).toHaveLength(0);
  expect(window.location.pathname).toBe("/ingresar");
});


test("MFA choice receives focus and both paths permit returning", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "student", mfa: "not_required" })), "POST /auth/invitations/accept": fail(403, "mfa_required"), "POST /auth/mfa/enrollment": fail(409, "already_enrolled") });
  renderAt("/invitacion#token=choice-invite");
  fireEvent.click(await screen.findByRole("button", { name: "Aceptar invitaci\u00f3n" }));
  const alert = await screen.findByRole("alert");
  await waitFor(() => expect(document.activeElement).toBe(alert));
  fireEvent.click(screen.getByRole("button", { name: "Activar verificaci\u00f3n en dos pasos" }));
  await screen.findByText(/ya est\u00e1 activada/i);
  fireEvent.click(screen.getByRole("button", { name: /volver/i }));
  fireEvent.click(await screen.findByRole("button", { name: "Ya tengo verificaci\u00f3n en dos pasos" }));
  fireEvent.click(await screen.findByRole("button", { name: /volver/i }));
  expect(await screen.findByRole("button", { name: "Activar verificaci\u00f3n en dos pasos" })).toBeTruthy();
});

test("invalid or abandoned invitation is not reused", async () => {
  mockApi({ "GET /auth/me": fail(401, "unauthenticated"), "POST /auth/invitations/accept": fail(422, "invalid_token") });
  const first = renderAt("/invitacion#token=expired-invite");
  fireEvent.change(await screen.findByLabelText("Eleg\u00ed tu contrase\u00f1a"), { target: { value: "una-clave-segura" } });
  fireEvent.click(screen.getByRole("button", { name: "Aceptar invitaci\u00f3n" }));
  await screen.findByText(/no es v\u00e1lido o ya venci\u00f3/i);
  first.unmount();
  const second = renderAt("/invitacion");
  expect(await screen.findByText(/Este enlace est\u00e1 incompleto/i)).toBeTruthy();
  second.unmount();
  const third = renderAt("/invitacion#token=abandoned-invite");
  await screen.findByLabelText("Eleg\u00ed tu contrase\u00f1a");
  third.unmount();
  renderAt("/invitacion");
  expect(await screen.findByText(/Este enlace est\u00e1 incompleto/i)).toBeTruthy();
});


test("pending MFA session returns to the same invitation", async () => {
  const sent = mockApi({ "GET /auth/me": [ok(me({ role: "student", mfa: "pending" })), ok(me({ role: "student", mfa: "satisfied" })), ok(me({ role: "veedor", mfa: "satisfied" }))], "POST /auth/mfa/verify": noContent(), "POST /auth/invitations/accept": ok({}, 200) });
  renderAt("/invitacion#token=pending-mfa-invite");
  fireEvent.change(await screen.findByLabelText("C\u00f3digo de 6 d\u00edgitos"), { target: { value: "123456" } });
  fireEvent.click(screen.getByRole("button", { name: "Verificar" }));
  fireEvent.click(await screen.findByRole("button", { name: "Aceptar invitaci\u00f3n" }));
  await waitFor(() => expect(sentTo(sent, "POST /auth/invitations/accept")[0]?.body).toEqual({ token: "pending-mfa-invite" }));
});
