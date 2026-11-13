import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, test } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, noContent, ok, sentTo } from "./test/api.ts";

function renderAt(url: string) {
  window.history.replaceState(null, "", url);
  return render(<App />);
}

function type(label: string | RegExp, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

async function expectPath(path: string) {
  await waitFor(() => expect(window.location.pathname).toBe(path));
}

/** The text of the form's live region (errors and confirmations are announced there). */
function liveText(): string {
  return Array.from(document.querySelectorAll("[aria-live]"))
    .map((el) => el.textContent ?? "")
    .join(" ");
}

describe("sign-in (/ingresar)", () => {
  test.each([
    ["not_required", "/", "Hola, ana@escuela.edu.ar"],
    ["enrollment_required", "/seguridad/activar", "Activá la verificación en dos pasos"],
    ["pending", "/seguridad/verificar", "Verificá que sos vos"],
  ] as const)("password sign-in with mfa %s goes to %s", async (mfa, path, heading) => {
    const sent = mockApi({
      "POST /auth/login": noContent(),
      "GET /auth/me": [fail(401, "unauthenticated"), ok(me({ mfa, role: mfa === "not_required" ? "student" : "officer" }))],
      "POST /auth/mfa/enrollment": ok({ secret: "JBSWY3DPEHPK3PXP", otpauthUri: "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP" }),
    });
    renderAt("/ingresar");
    await screen.findByRole("heading", { level: 1, name: "Ingresá" });

    type("Mail", "ana@escuela.edu.ar");
    type("Contraseña", "una-clave-larga");
    fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

    await expectPath(path);
    expect(await screen.findByRole("heading", { level: 1, name: heading })).toBeTruthy();
    expect(sentTo(sent, "POST /auth/login")[0]?.body).toEqual({
      email: "ana@escuela.edu.ar",
      password: "una-clave-larga",
    });
  });

  test("invalid credentials show a Spanish message in the live region and stay on the form", async () => {
    mockApi({ "POST /auth/login": fail(401, "invalid_credentials") });
    renderAt("/ingresar");
    await screen.findByRole("heading", { name: "Ingresá" });

    type("Mail", "ana@escuela.edu.ar");
    type("Contraseña", "equivocada-123");
    fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

    await waitFor(() => expect(liveText()).toContain("El mail o la contraseña no coinciden."));
    expect(liveText()).not.toContain("invalid_credentials");
    expect(window.location.pathname).toBe("/ingresar");
    expect((screen.getByLabelText("Contraseña") as HTMLInputElement).value).toBe("equivocada-123");
  });

  test("requesting a sign-in link always shows the same neutral confirmation", async () => {
    const sent = mockApi({ "POST /auth/link": noContent(202) });
    renderAt("/ingresar");
    await screen.findByRole("heading", { name: "Ingresá" });

    type("Mail", "quien@sea.com");
    fireEvent.click(screen.getByRole("button", { name: "Enviarme un enlace para ingresar" }));

    await waitFor(() =>
      expect(liveText()).toContain(
        "Si el mail tiene una cuenta, te enviamos un enlace para ingresar. Vence en 15 minutos.",
      ),
    );
    expect(sentTo(sent, "POST /auth/link")[0]?.body).toEqual({ email: "quien@sea.com" });
  });

  test("a #token= sign-in link is removed from the URL before any request and consumed once", async () => {
    const sent = mockApi({
      "POST /auth/link/consume": noContent(),
      "GET /auth/me": [fail(401, "unauthenticated"), ok(me())],
    });
    renderAt("/ingresar#token=tok-abc");

    expect(window.location.hash).toBe("");
    expect(window.location.pathname).toBe("/ingresar");

    await expectPath("/");
    expect(await screen.findByRole("heading", { name: "Hola, ana@escuela.edu.ar" })).toBeTruthy();
    const consumed = sentTo(sent, "POST /auth/link/consume");
    expect(consumed).toHaveLength(1);
    expect(consumed[0]?.body).toEqual({ token: "tok-abc" });
    expect(sent.every((s) => !s.href.includes("#"))).toBe(true);
  });

  test("an expired sign-in link explains what to do", async () => {
    mockApi({ "POST /auth/link/consume": fail(422, "invalid_token") });
    renderAt("/ingresar#token=old");
    await waitFor(() => expect(liveText()).toContain("El enlace no es válido o ya venció. Pedí uno nuevo."));
    expect(window.location.pathname).toBe("/ingresar");
  });
});

describe("T6a advisories: sign-in edges", () => {
  test("a sign-in link opened while signed in asks before replacing the current session", async () => {
    const sent = mockApi({
      "GET /auth/me": [ok(me()), ok(me({ email: "otra@escuela.edu.ar" }))],
      "POST /auth/link/consume": noContent(),
    });
    renderAt("/ingresar#token=tok-2");
    expect(await screen.findByText(/Ya ingresaste como ana@escuela.edu.ar/)).toBeTruthy();
    expect(sentTo(sent, "POST /auth/link/consume")).toHaveLength(0);

    fireEvent.click(screen.getByRole("button", { name: "Ingresar con el enlace" }));
    expect(await screen.findByRole("heading", { name: "Hola, otra@escuela.edu.ar" })).toBeTruthy();
    expect(sentTo(sent, "POST /auth/link/consume").map((s) => s.body)).toEqual([{ token: "tok-2" }]);
  });

  test("keeping the current session ignores the link", async () => {
    const sent = mockApi({ "GET /auth/me": ok(me()) });
    renderAt("/ingresar#token=tok-3");
    fireEvent.click(await screen.findByRole("button", { name: "Seguir como ana@escuela.edu.ar" }));
    await expectPath("/");
    expect(sentTo(sent, "POST /auth/link/consume")).toHaveLength(0);
  });

  test("if loading the user fails right after sign-in, the retry screen is shown", async () => {
    mockApi({
      "POST /auth/login": noContent(),
      "GET /auth/me": [fail(401, "unauthenticated"), fail(503, "unavailable"), ok(me())],
    });
    renderAt("/ingresar");
    await screen.findByRole("heading", { name: "Ingresá" });
    type("Mail", "ana@escuela.edu.ar");
    type("Contraseña", "una-clave-larga");
    fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

    fireEvent.click(await screen.findByRole("button", { name: "Reintentar" }));
    expect(await screen.findByRole("heading", { name: "Hola, ana@escuela.edu.ar" })).toBeTruthy();
  });

  test("a failed two-factor enrollment start offers «Reintentar»", async () => {
    const sent = mockApi({
      "GET /auth/me": ok(me({ role: "school_admin", mfa: "enrollment_required" })),
      "POST /auth/mfa/enrollment": [
        fail(503, "unavailable"),
        ok({ secret: "JBSWY3DPEHPK3PXP", otpauthUri: "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP" }),
      ],
    });
    renderAt("/seguridad/activar");
    await waitFor(() => expect(liveText()).toContain("Algo salió mal de nuestro lado."));
    fireEvent.click(screen.getByRole("button", { name: "Reintentar" }));
    expect(await screen.findByText("JBSW Y3DP EHPK 3PXP")).toBeTruthy();
    expect(sentTo(sent, "POST /auth/mfa/enrollment")).toHaveLength(2);
  });
});

describe("claim and redeem (/alta)", () => {
  test("formats the access code as XXXX-XXXX-XXXX and creates the account", async () => {
    const sent = mockApi({
      "POST /auth/claim": ok(me(), 201),
      "GET /auth/me": [fail(401, "unauthenticated"), ok(me())],
    });
    renderAt("/alta");
    await screen.findByRole("heading", { name: "Activá tu cuenta" });

    type("Código de acceso", "abcd efgh-jkmn9");
    expect((screen.getByLabelText("Código de acceso") as HTMLInputElement).value).toBe("ABCD-EFGH-JKMN");
    type("Mail", "ana@escuela.edu.ar");
    type("Contraseña", "una-clave-larga");
    fireEvent.click(screen.getByRole("button", { name: "Crear cuenta" }));

    await expectPath("/");
    expect(sentTo(sent, "POST /auth/claim")[0]?.body).toEqual({
      code: "ABCD-EFGH-JKMN",
      email: "ana@escuela.edu.ar",
      password: "una-clave-larga",
    });
  });

  test("email_in_use tells the student to sign in and redeem instead", async () => {
    mockApi({ "POST /auth/claim": fail(409, "email_in_use") });
    renderAt("/alta");
    await screen.findByRole("heading", { name: "Activá tu cuenta" });

    type("Código de acceso", "ABCDEFGHJKMN");
    type("Mail", "ana@escuela.edu.ar");
    type("Contraseña", "una-clave-larga");
    fireEvent.click(screen.getByRole("button", { name: "Crear cuenta" }));

    await waitFor(() =>
      expect(liveText()).toContain("Ya hay una cuenta con ese mail. Ingresá con ella y después canjeá el código."),
    );
    expect(window.location.pathname).toBe("/alta");
  });

  test("when signed in, the same page redeems the code without asking for email or password", async () => {
    const sent = mockApi({
      "GET /auth/me": ok(me()),
      "POST /auth/redeem": ok(me({ tenantId: "t2" })),
    });
    renderAt("/alta");
    await screen.findByRole("heading", { name: "Sumá tu escuela" });
    expect(screen.queryByLabelText("Mail")).toBeNull();
    expect(screen.queryByLabelText("Contraseña")).toBeNull();

    type("Código de acceso", "abcd-efgh-jkmn");
    fireEvent.click(screen.getByRole("button", { name: "Canjear código" }));

    await expectPath("/");
    expect(sentTo(sent, "POST /auth/redeem")[0]?.body).toEqual({ code: "ABCD-EFGH-JKMN" });
  });
});

describe("emailed tokens", () => {
  test("/verificar#token= verifies the email and strips the fragment", async () => {
    const sent = mockApi({ "POST /auth/email/verify": noContent() });
    renderAt("/verificar#token=v-1");
    expect(window.location.hash).toBe("");
    expect(await screen.findByRole("heading", { name: "¡Mail verificado!" })).toBeTruthy();
    expect(sentTo(sent, "POST /auth/email/verify")[0]?.body).toEqual({ token: "v-1" });
  });

  test("/restablecer without a token emails a reset link with a neutral confirmation", async () => {
    const sent = mockApi({ "POST /auth/password/reset-request": noContent(202) });
    renderAt("/restablecer");
    await screen.findByRole("heading", { name: "Restablecé tu contraseña" });
    type("Mail", "ana@escuela.edu.ar");
    fireEvent.click(screen.getByRole("button", { name: "Enviarme el enlace" }));
    await waitFor(() =>
      expect(liveText()).toContain(
        "Si el mail tiene una cuenta, te enviamos un enlace para elegir una contraseña nueva. Vence en 30 minutos.",
      ),
    );
    expect(sentTo(sent, "POST /auth/password/reset-request")[0]?.body).toEqual({ email: "ana@escuela.edu.ar" });
  });

  test("reset with weak_password keeps the form and the token for another try", async () => {
    const sent = mockApi({
      "POST /auth/password/reset": [fail(422, "weak_password"), noContent()],
    });
    renderAt("/restablecer#token=rt-1");
    expect(window.location.hash).toBe("");
    await screen.findByRole("heading", { name: "Elegí una contraseña nueva" });

    type("Contraseña nueva", "corta");
    fireEvent.click(screen.getByRole("button", { name: "Guardar contraseña" }));
    await waitFor(() => expect(liveText()).toContain("La contraseña tiene que tener entre 10 y 128 caracteres."));
    expect(screen.getByLabelText("Contraseña nueva")).toBeTruthy();

    type("Contraseña nueva", "una-clave-larga");
    fireEvent.click(screen.getByRole("button", { name: "Guardar contraseña" }));
    await waitFor(() => expect(liveText()).toContain("Listo, ya podés ingresar con tu contraseña nueva."));
    expect(sentTo(sent, "POST /auth/password/reset").map((s) => s.body)).toEqual([
      { token: "rt-1", password: "corta" },
      { token: "rt-1", password: "una-clave-larga" },
    ]);
  });
});

describe("two-factor authentication", () => {
  test("enrollment shows the secret, confirms a code and requires saving recovery codes before continuing", async () => {
    const codes = Array.from({ length: 10 }, (_, i) => `AAAA${i}-BBBB${i}`);
    const sent = mockApi({
      "GET /auth/me": [ok(me({ role: "school_admin", mfa: "enrollment_required" })), ok(me({ role: "school_admin", mfa: "satisfied" }))],
      "POST /auth/mfa/enrollment": ok({
        secret: "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP",
        otpauthUri: "otpauth://totp/Centro%20de%20Estudiantes:ana?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP",
      }),
      "POST /auth/mfa/enrollment/confirm": ok({ recoveryCodes: codes }),
    });
    renderAt("/seguridad/activar");

    expect(await screen.findByText("JBSW Y3DP EHPK 3PXP JBSW Y3DP EHPK 3PXP")).toBeTruthy();
    const open = screen.getByRole("link", { name: "Abrir en la app de autenticación" });
    expect(open.getAttribute("href")).toMatch(/^otpauth:\/\/totp\//);
    expect(screen.getByRole("button", { name: "Copiar clave" })).toBeTruthy();

    type("Código de 6 dígitos", "123456");
    fireEvent.click(screen.getByRole("button", { name: "Activar" }));

    const list = await screen.findByRole("list", { name: "Códigos de recuperación" });
    expect(within(list).getAllByRole("listitem").map((li) => li.textContent)).toEqual(codes);
    expect(sentTo(sent, "POST /auth/mfa/enrollment/confirm")[0]?.body).toEqual({ code: "123456" });

    fireEvent.click(screen.getByRole("button", { name: "Continuar" }));
    await waitFor(() => expect(liveText()).toContain("Marcá la casilla cuando hayas guardado los códigos."));
    expect(window.location.pathname).toBe("/seguridad/activar");

    fireEvent.click(screen.getByLabelText("Ya los guardé en un lugar seguro"));
    fireEvent.click(screen.getByRole("button", { name: "Continuar" }));
    await expectPath("/");
  });

  test("a pending session can verify with a recovery code", async () => {
    const sent = mockApi({
      "GET /auth/me": [ok(me({ role: "officer", mfa: "pending" })), ok(me({ role: "officer", mfa: "satisfied" }))],
      "POST /auth/mfa/verify": noContent(),
    });
    renderAt("/seguridad/verificar");
    await screen.findByRole("heading", { name: "Verificá que sos vos" });

    fireEvent.click(screen.getByRole("button", { name: "Usar un código de recuperación" }));
    type("Código de recuperación", "abcde-fghjk");
    fireEvent.click(screen.getByRole("button", { name: "Verificar" }));

    await expectPath("/");
    expect(sentTo(sent, "POST /auth/mfa/verify")[0]?.body).toEqual({ recoveryCode: "abcde-fghjk" });
  });

  test("a wrong two-factor code shows a Spanish message", async () => {
    mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "pending" })),
      "POST /auth/mfa/verify": fail(422, "invalid_code"),
    });
    renderAt("/seguridad/verificar");
    await screen.findByRole("heading", { name: "Verificá que sos vos" });
    type("Código de 6 dígitos", "000000");
    fireEvent.click(screen.getByRole("button", { name: "Verificar" }));
    await waitFor(() =>
      expect(liveText()).toContain("El código no es correcto o ya se usó. Probá con el próximo que muestre tu app."),
    );
  });
});
