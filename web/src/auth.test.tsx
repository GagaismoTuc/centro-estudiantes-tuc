import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { encodeQr } from "./lib/qr/encode.ts";
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

/** The modules drawn by a QR SVG (one `M x y h1v1h-1z` square per dark module inside a 4-module quiet zone). */
function qrModules(svg: Element): boolean[][] {
  const size = Number(svg.getAttribute("viewBox")!.split(" ")[2]) - 8;
  const modules = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  for (const [, x, y] of svg.querySelector("path")!.getAttribute("d")!.matchAll(/M(\d+) (\d+)h1v1h-1z/g)) {
    modules[Number(y) - 4]![Number(x) - 4] = true;
  }
  return modules;
}

describe("sign-in (/ingresar)", () => {
  test.each([
    ["not_required", "/", "¡Hola!"],
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
    expect(await screen.findByRole("heading", { name: "¡Hola!" })).toBeTruthy();
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
    expect((await screen.findAllByText("otra@escuela.edu.ar")).length).toBeGreaterThan(0);
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
    expect(await screen.findByRole("heading", { name: "¡Hola!" })).toBeTruthy();
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

    type("Contraseña nueva", "rechazada-por-el-servidor");
    fireEvent.click(screen.getByRole("button", { name: "Guardar contraseña" }));
    await waitFor(() => expect(liveText()).toContain("La contraseña tiene que tener entre 10 y 128 caracteres."));
    expect(screen.getByLabelText("Contraseña nueva")).toBeTruthy();

    type("Contraseña nueva", "una-clave-larga");
    fireEvent.click(screen.getByRole("button", { name: "Guardar contraseña" }));
    await waitFor(() => expect(liveText()).toContain("Listo, ya podés ingresar con tu contraseña nueva."));
    expect(sentTo(sent, "POST /auth/password/reset").map((s) => s.body)).toEqual([
      { token: "rt-1", password: "rechazada-por-el-servidor" },
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
    expect(await screen.findByText("Marcá la casilla cuando hayas guardado los códigos.")).toBeTruthy();
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


/** The inline error the field points to through aria-describedby. */
function fieldError(input: HTMLElement): string {
  const ids = (input.getAttribute("aria-describedby") ?? "").split(" ").filter(Boolean);
  return ids.map((id) => document.getElementById(id)?.textContent ?? "").join(" ");
}

/** Submits like pressing Enter in a field (implicit submission, no submitter). */
function pressEnter(input: HTMLElement) {
  (input as HTMLInputElement).form?.requestSubmit();
}

describe("T3 Spanish form validation (A5) and focus after errors (A8)", () => {
  test("an empty sign-in shows Spanish messages next to the fields and focuses the first one", async () => {
    const sent = mockApi({});
    renderAt("/ingresar");
    await screen.findByRole("heading", { name: "Ingresá" });
    const email = screen.getByLabelText("Mail");
    expect(email.closest("form")?.noValidate).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

    await waitFor(() => expect(email.getAttribute("aria-invalid")).toBe("true"));
    expect(fieldError(email)).toContain("Escribí tu mail.");
    const password = screen.getByLabelText("Contraseña");
    expect(password.getAttribute("aria-invalid")).toBe("true");
    expect(fieldError(password)).toContain("Escribí tu contraseña.");
    expect(document.activeElement).toBe(email);
    expect(sentTo(sent, "POST /auth/login")).toHaveLength(0);

    type("Mail", "ana@escuela.edu.ar");
    await waitFor(() => expect(email.getAttribute("aria-invalid")).toBeNull());
  });

  test("a malformed email is caught before sending a sign-in link", async () => {
    const sent = mockApi({});
    renderAt("/ingresar");
    await screen.findByRole("heading", { name: "Ingresá" });
    type("Mail", "ana-escuela");
    fireEvent.click(screen.getByRole("button", { name: "Enviarme un enlace para ingresar" }));
    const email = screen.getByLabelText("Mail");
    await waitFor(() => expect(fieldError(email)).toContain("Revisá el mail"));
    expect(document.activeElement).toBe(email);
    expect(sent.filter((s) => s.path !== "/auth/me")).toHaveLength(0);
  });

  test("claim with Enter validates code, email and password length in Spanish", async () => {
    const sent = mockApi({});
    renderAt("/alta");
    await screen.findByRole("heading", { name: "Activá tu cuenta" });
    type("Código de acceso", "abcd");
    type("Mail", "ana@");
    type("Contraseña", "corta");
    pressEnter(screen.getByLabelText("Contraseña"));

    const code = screen.getByLabelText("Código de acceso");
    await waitFor(() => expect(code.getAttribute("aria-invalid")).toBe("true"));
    expect(fieldError(code)).toContain("El código tiene 12 letras y números");
    expect(fieldError(screen.getByLabelText("Mail"))).toContain("Revisá el mail");
    expect(fieldError(screen.getByLabelText("Contraseña"))).toContain("La contraseña tiene que tener entre 10 y 128 caracteres.");
    expect(document.activeElement).toBe(code);
    expect(sentTo(sent, "POST /auth/claim")).toHaveLength(0);
  });

  test("a too-short new password is caught before calling the API", async () => {
    const sent = mockApi({});
    renderAt("/restablecer#token=rt-9");
    await screen.findByRole("heading", { name: "Elegí una contraseña nueva" });
    type("Contraseña nueva", "corta");
    fireEvent.click(screen.getByRole("button", { name: "Guardar contraseña" }));
    const password = screen.getByLabelText("Contraseña nueva");
    await waitFor(() => expect(fieldError(password)).toContain("La contraseña tiene que tener entre 10 y 128 caracteres."));
    expect(document.activeElement).toBe(password);
    expect(sentTo(sent, "POST /auth/password/reset")).toHaveLength(0);
  });

  test("the recovery-codes checkbox explains itself next to the box and takes focus", async () => {
    mockApi({
      "GET /auth/me": ok(me({ role: "school_admin", mfa: "enrollment_required" })),
      "POST /auth/mfa/enrollment": ok({ secret: "JBSWY3DPEHPK3PXP", otpauthUri: "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP" }),
      "POST /auth/mfa/enrollment/confirm": ok({ recoveryCodes: ["AAAAA-BBBBB"] }),
    });
    renderAt("/seguridad/activar");
    await screen.findByText("JBSW Y3DP EHPK 3PXP");
    type("Código de 6 dígitos", "123456");
    fireEvent.click(screen.getByRole("button", { name: "Activar" }));
    await screen.findByRole("list", { name: "Códigos de recuperación" });

    fireEvent.click(screen.getByRole("button", { name: "Continuar" }));
    const box = screen.getByLabelText("Ya los guardé en un lugar seguro");
    await waitFor(() => expect(box.getAttribute("aria-invalid")).toBe("true"));
    expect(fieldError(box)).toContain("Marcá la casilla cuando hayas guardado los códigos.");
    expect(document.activeElement).toBe(box);
  });

  test("a wrong 6-digit code is caught in Spanish before calling the API", async () => {
    const sent = mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "pending" })) });
    renderAt("/seguridad/verificar");
    await screen.findByRole("heading", { name: "Verificá que sos vos" });
    type("Código de 6 dígitos", "12a");
    fireEvent.click(screen.getByRole("button", { name: "Verificar" }));
    const code = screen.getByLabelText("Código de 6 dígitos");
    await waitFor(() => expect(fieldError(code)).toContain("El código tiene 6 números."));
    expect(sentTo(sent, "POST /auth/mfa/verify")).toHaveLength(0);
  });

  test("invalid credentials mark both fields invalid and move focus to the first", async () => {
    mockApi({ "POST /auth/login": fail(401, "invalid_credentials") });
    renderAt("/ingresar");
    await screen.findByRole("heading", { name: "Ingresá" });
    type("Mail", "ana@escuela.edu.ar");
    type("Contraseña", "equivocada-123");
    fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

    const email = screen.getByLabelText("Mail");
    await waitFor(() => expect(document.activeElement).toBe(email));
    expect(email.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByLabelText("Contraseña").getAttribute("aria-invalid")).toBe("true");
    expect(fieldError(email)).toContain("El mail o la contraseña no coinciden.");
  });

  test("an error not tied to a field moves focus to the message, which says to wait about 15 minutes (A13)", async () => {
    mockApi({ "POST /auth/login": fail(429, "too_many_attempts") });
    renderAt("/ingresar");
    await screen.findByRole("heading", { name: "Ingresá" });
    type("Mail", "ana@escuela.edu.ar");
    type("Contraseña", "equivocada-123");
    fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

    await waitFor(() => expect(document.activeElement?.textContent).toContain("Esperá unos 15 minutos"));
    expect(document.activeElement).not.toBe(document.body);
  });
});

describe("T3 copy fixes", () => {
  test("A7: a wrong recovery code does not talk about the app", async () => {
    mockApi({
      "GET /auth/me": ok(me({ role: "officer", mfa: "pending" })),
      "POST /auth/mfa/verify": fail(422, "invalid_code"),
    });
    renderAt("/seguridad/verificar");
    await screen.findByRole("heading", { name: "Verificá que sos vos" });
    fireEvent.click(screen.getByRole("button", { name: "Usar un código de recuperación" }));
    type("Código de recuperación", "abcde-fghjk");
    fireEvent.click(screen.getByRole("button", { name: "Verificar" }));
    await waitFor(() => expect(liveText()).toContain("El código de recuperación no es correcto o ya se usó."));
    expect(liveText()).not.toContain("tu app");
  });

  test("A10: /restablecer does not claim the mail was sent before sending it", async () => {
    mockApi({});
    renderAt("/restablecer");
    const card = (await screen.findByRole("heading", { name: "Restablecé tu contraseña" })).closest("main") as HTMLElement;
    expect(card.textContent).not.toMatch(/Te mandamos|Te enviamos/i);
  });

  test("A11: a used or expired verification link says so once, without repeating the advice", async () => {
    mockApi({ "POST /auth/email/verify": fail(422, "invalid_token") });
    renderAt("/verificar#token=used");
    await waitFor(() => expect(liveText()).toContain("El enlace ya se usó o venció."));
    const main = screen.getByRole("main");
    expect(main.textContent?.match(/nuevo/g) ?? []).toHaveLength(1);
  });

  test("A11: a failed verification gives the reason before the next step", async () => {
    mockApi({ "POST /auth/email/verify": fail(422, "invalid_token") });
    renderAt("/verificar#token=used");
    const reason = await screen.findByText("El enlace ya se usó o venció.", { exact: false });
    const hint = screen.getByText("Ingresá y desde el inicio pedí un enlace nuevo.");
    expect(reason.compareDocumentPosition(hint) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  test("A11: a new #token arriving on /verificar is processed", async () => {
    const sent = mockApi({ "POST /auth/email/verify": [fail(422, "invalid_token"), noContent()] });
    renderAt("/verificar#token=old");
    await waitFor(() => expect(liveText()).toContain("El enlace ya se usó o venció."));

    window.history.replaceState(null, "", "/verificar#token=fresh");
    window.dispatchEvent(new HashChangeEvent("hashchange"));

    expect(await screen.findByRole("heading", { name: "¡Mail verificado!" })).toBeTruthy();
    expect(window.location.hash).toBe("");
    expect(sentTo(sent, "POST /auth/email/verify").map((s) => s.body)).toEqual([{ token: "old" }, { token: "fresh" }]);
  });

  test("A11: a new #token arriving on /ingresar signs in with it", async () => {
    const sent = mockApi({
      "POST /auth/link/consume": noContent(),
      "GET /auth/me": [fail(401, "unauthenticated"), ok(me())],
    });
    renderAt("/ingresar");
    await screen.findByRole("heading", { name: "Ingresá" });

    window.history.replaceState(null, "", "/ingresar#token=tok-new");
    window.dispatchEvent(new HashChangeEvent("hashchange"));

    await expectPath("/");
    expect(sentTo(sent, "POST /auth/link/consume").map((s) => s.body)).toEqual([{ token: "tok-new" }]);
  });

  test("R4: the enrollment QR is an image labelled for the authenticator app, with the secret beside it", async () => {
    const uri =
      "otpauth://totp/Centro%20de%20Estudiantes:ana@example.com?secret=JBSWY3DPEHPK3PXP&issuer=Centro%20de%20Estudiantes&algorithm=SHA1&digits=6&period=30";
    mockApi({
      "GET /auth/me": ok(me({ role: "school_admin", mfa: "enrollment_required" })),
      "POST /auth/mfa/enrollment": ok({ secret: "JBSWY3DPEHPK3PXP", otpauthUri: uri }),
    });
    renderAt("/seguridad/activar");

    const qr = await screen.findByRole("img", { name: "Código QR para tu app de autenticación" });
    expect(qr.tagName).toBe("svg");
    expect(qrModules(qr)).toEqual(encodeQr(uri).modules);
    const step = qr.closest("li")!;
    expect(within(step).getByText("JBSW Y3DP EHPK 3PXP")).toBeTruthy();
    expect(within(step).getByRole("button", { name: "Copiar clave" })).toBeTruthy();
  });

  test("R5: the otpauth URI is never logged or stored, and the QR disappears once enrollment is confirmed", async () => {
    const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP";
    const uri = `otpauth://totp/Centro%20de%20Estudiantes:ana@example.com?secret=${secret}&issuer=Centro%20de%20Estudiantes&algorithm=SHA1&digits=6&period=30`;
    const spies = (["log", "info", "warn", "error", "debug"] as const).map((method) =>
      vi.spyOn(console, method).mockImplementation(() => {}),
    );
    const stored = () => Object.keys(localStorage).map((key) => `${key}=${localStorage.getItem(key)}`).join(" ");
    try {
      mockApi({
        "GET /auth/me": ok(me({ role: "school_admin", mfa: "enrollment_required" })),
        "POST /auth/mfa/enrollment": ok({ secret, otpauthUri: uri }),
        "POST /auth/mfa/enrollment/confirm": ok({ recoveryCodes: ["AAAAA-BBBBB"] }),
      });
      renderAt("/seguridad/activar");
      await screen.findByRole("img", { name: "Código QR para tu app de autenticación" });
      expect(stored()).not.toContain(secret);

      type("Código de 6 dígitos", "123456");
      fireEvent.click(screen.getByRole("button", { name: "Activar" }));
      await screen.findByRole("list", { name: "Códigos de recuperación" });

      expect(screen.queryByRole("img", { name: "Código QR para tu app de autenticación" })).toBeNull();
      expect(document.querySelector("svg.qr")).toBeNull();
      expect(stored()).not.toContain(secret);
      expect(JSON.stringify(spies.flatMap((spy) => spy.mock.calls))).not.toContain(secret);
    } finally {
      spies.forEach((spy) => spy.mockRestore());
    }
  });
});

/** Creates an isolated Web Storage instance representing another browser tab's sessionStorage. */
function createTabStorage(): Storage {
  const store = new Map<string, string>();
  return {
    get length() {
      return store.size;
    },
    clear() {
      store.clear();
    },
    getItem(key: string) {
      return store.get(key) ?? null;
    },
    key(index: number) {
      return Array.from(store.keys())[index] ?? null;
    },
    removeItem(key: string) {
      store.delete(key);
    },
    setItem(key: string, value: string) {
      store.set(key, String(value));
    },
  };
}

function storageValues(storage: Storage): Array<string | null> {
  return Array.from({ length: storage.length }, (_, i) => storage.getItem(storage.key(i) ?? ""));
}

/** Runs `fn` with `sessionStorage` bound to an isolated tab storage while keeping `localStorage` shared. */
async function withIsolatedTabSessionStorage<T>(tabStorage: Storage, fn: () => Promise<T>): Promise<T> {
  const prevGlobal = Object.getOwnPropertyDescriptor(globalThis, "sessionStorage");
  const prevWindow = Object.getOwnPropertyDescriptor(window, "sessionStorage");
  Object.defineProperty(globalThis, "sessionStorage", { configurable: true, value: tabStorage });
  Object.defineProperty(window, "sessionStorage", { configurable: true, value: tabStorage });
  try {
    return await fn();
  } finally {
    if (prevGlobal) Object.defineProperty(globalThis, "sessionStorage", prevGlobal);
    else Reflect.deleteProperty(globalThis, "sessionStorage");
    if (prevWindow) Object.defineProperty(window, "sessionStorage", prevWindow);
    else Reflect.deleteProperty(window, "sessionStorage");
  }
}

describe("return to requested screen (next)", () => {
  test("password sign-in returns to next", async () => {
    mockApi({
      "POST /auth/login": noContent(),
      "GET /auth/me": [fail(401, "unauthenticated"), ok(me({ role: "student", mfa: "not_required" }))],
    });
    renderAt("/seguridad");
    await expectPath("/ingresar");
    expect(window.location.search).toBe("?next=%2Fseguridad");
    await screen.findByRole("heading", { level: 1, name: "Ingresá" });

    type("Mail", "ana@escuela.edu.ar");
    type("Contraseña", "una-clave-larga");
    fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

    await expectPath("/seguridad");
    expect(window.location.search).toBe("");
    expect(await screen.findByRole("heading", { level: 1, name: "Seguridad" })).toBeTruthy();
  });

  test("password sign-in with two-factor verification returns to next", async () => {
    mockApi({
      "POST /auth/login": noContent(),
      "POST /auth/mfa/verify": noContent(),
      "GET /auth/me": [
        fail(401, "unauthenticated"),
        ok(me({ role: "school_admin", mfa: "pending" })),
        ok(me({ role: "school_admin", mfa: "satisfied" })),
      ],
      "GET /roster": ok([]),
    });
    renderAt("/padron");
    await expectPath("/ingresar");
    await screen.findByRole("heading", { level: 1, name: "Ingresá" });

    type("Mail", "ana@escuela.edu.ar");
    type("Contraseña", "una-clave-larga");
    fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

    await expectPath("/seguridad/verificar");
    expect(window.location.search).toBe("?next=%2Fpadron");
    await screen.findByRole("heading", { level: 1, name: "Verificá que sos vos" });
    type("Código de 6 dígitos", "123456");
    fireEvent.click(screen.getByRole("button", { name: "Verificar" }));

    await expectPath("/padron");
    expect(window.location.search).toBe("");
    expect(await screen.findByRole("heading", { level: 1, name: "Padrón" })).toBeTruthy();
  });

  test("password sign-in with two-factor enrollment returns to next", async () => {
    mockApi({
      "POST /auth/login": noContent(),
      "POST /auth/mfa/enrollment": ok({ secret: "JBSWY3DPEHPK3PXP", otpauthUri: "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP" }),
      "POST /auth/mfa/enrollment/confirm": ok({ recoveryCodes: ["AAAAA-BBBBB"] }),
      "GET /auth/me": [
        fail(401, "unauthenticated"),
        ok(me({ role: "school_admin", mfa: "enrollment_required" })),
        ok(me({ role: "school_admin", mfa: "satisfied" })),
      ],
      "GET /roster": ok([]),
    });
    renderAt("/padron");
    await expectPath("/ingresar");
    await screen.findByRole("heading", { level: 1, name: "Ingresá" });

    type("Mail", "ana@escuela.edu.ar");
    type("Contraseña", "una-clave-larga");
    fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

    await expectPath("/seguridad/activar");
    expect(window.location.search).toBe("?next=%2Fpadron");
    await screen.findByText("JBSW Y3DP EHPK 3PXP");
    type("Código de 6 dígitos", "123456");
    fireEvent.click(screen.getByRole("button", { name: "Activar" }));

    fireEvent.click(await screen.findByLabelText("Ya los guardé en un lugar seguro"));
    fireEvent.click(screen.getByRole("button", { name: "Continuar" }));

    await expectPath("/padron");
    expect(window.location.search).toBe("");
    expect(await screen.findByRole("heading", { level: 1, name: "Padrón" })).toBeTruthy();
  });

  test.each(["https://evil.example", "//evil.example", "javascript:alert(1)", "/\\evil.example"])(
    "unsafe next=%s falls back to / after sign-in",
    async (badNext) => {
      mockApi({
        "POST /auth/login": noContent(),
        "GET /auth/me": [fail(401, "unauthenticated"), ok(me())],
      });
      renderAt(`/ingresar?next=${encodeURIComponent(badNext)}`);
      await screen.findByRole("heading", { level: 1, name: "Ingresá" });

      type("Mail", "ana@escuela.edu.ar");
      type("Contraseña", "una-clave-larga");
      fireEvent.click(screen.getByRole("button", { name: "Ingresar" }));

      await expectPath("/");
      expect(window.location.search).toBe("");
      expect(await screen.findByRole("heading", { level: 1, name: "¡Hola!" })).toBeTruthy();
    },
  );

  test("requesting a sign-in link saves next in sessionStorage, uses and clears it in the same browser, and never sends next to the server", async () => {
    const sent1 = mockApi({ "POST /auth/link": noContent(202) });
    const first = renderAt("/padron");
    await expectPath("/ingresar");
    expect(window.location.search).toBe("?next=%2Fpadron");
    await screen.findByRole("heading", { level: 1, name: "Ingresá" });

    type("Mail", "ana@escuela.edu.ar");
    fireEvent.click(screen.getByRole("button", { name: "Enviarme un enlace para ingresar" }));
    await waitFor(() =>
      expect(liveText()).toContain(
        "Si el mail tiene una cuenta, te enviamos un enlace para ingresar. Vence en 15 minutos.",
      ),
    );
    expect(sentTo(sent1, "POST /auth/link")[0]?.body).toEqual({ email: "ana@escuela.edu.ar" });
    expect(sent1.every((s) => !s.path.includes("next") && !JSON.stringify(s.body ?? "").includes("padron"))).toBe(true);
    expect(storageValues(sessionStorage)).toContain("/padron");
    first.unmount();

    const sent2 = mockApi({
      "POST /auth/link/consume": noContent(),
      "GET /auth/me": [fail(401, "unauthenticated"), ok(me({ role: "school_admin", mfa: "satisfied" }))],
      "GET /roster": ok([]),
    });
    renderAt("/ingresar#token=tok-same");

    await expectPath("/padron");
    expect(window.location.search).toBe("");
    expect(await screen.findByRole("heading", { level: 1, name: "Padrón" })).toBeTruthy();
    expect(sentTo(sent2, "POST /auth/link/consume")[0]?.body).toEqual({ token: "tok-same" });
    expect(sessionStorage.length).toBe(0);
  });

  test("opening a sign-in link in a new tab with isolated sessionStorage goes to / even when the original tab has next stored", async () => {
    mockApi({ "POST /auth/link": noContent(202) });
    const originalTab = renderAt("/padron");
    await expectPath("/ingresar");
    expect(window.location.search).toBe("?next=%2Fpadron");
    await screen.findByRole("heading", { level: 1, name: "Ingresá" });

    type("Mail", "ana@escuela.edu.ar");
    fireEvent.click(screen.getByRole("button", { name: "Enviarme un enlace para ingresar" }));
    await waitFor(() =>
      expect(liveText()).toContain(
        "Si el mail tiene una cuenta, te enviamos un enlace para ingresar. Vence en 15 minutos.",
      ),
    );
    const originalStorage = window.sessionStorage;
    expect(storageValues(originalStorage)).toContain("/padron");
    originalTab.unmount();

    const newTabStorage = createTabStorage();
    expect(newTabStorage).not.toBe(originalStorage);
    expect(newTabStorage.length).toBe(0);

    await withIsolatedTabSessionStorage(newTabStorage, async () => {
      mockApi({
        "POST /auth/link/consume": noContent(),
        "GET /auth/me": [fail(401, "unauthenticated"), ok(me({ role: "school_admin", mfa: "satisfied" }))],
        "GET /roster": ok([]),
      });
      const newTab = renderAt("/ingresar#token=tok-other");

      await expectPath("/");
      expect(window.location.search).toBe("");
      expect(await screen.findByRole("heading", { level: 1, name: "¡Hola!" })).toBeTruthy();
      newTab.unmount();
    });

    // The original tab's sessionStorage stays intact and still returns to /padron if opened there.
    expect(storageValues(originalStorage)).toContain("/padron");
    mockApi({
      "POST /auth/link/consume": noContent(),
      "GET /auth/me": [fail(401, "unauthenticated"), ok(me({ role: "school_admin", mfa: "satisfied" }))],
      "GET /roster": ok([]),
    });
    renderAt("/ingresar#token=tok-original");
    await expectPath("/padron");
    expect(window.location.search).toBe("");
    expect(await screen.findByRole("heading", { level: 1, name: "Padrón" })).toBeTruthy();
    expect(originalStorage.length).toBe(0);
  });
});

