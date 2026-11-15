import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, test } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, noContent, ok, sentTo } from "./test/api.ts";

async function renderSecurity() {
  window.history.replaceState(null, "", "/seguridad");
  render(<App />);
  await screen.findByRole("heading", { level: 1, name: "Seguridad" });
}

function type(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

function submit() {
  fireEvent.click(screen.getByRole("button", { name: "Cambiar contraseña" }));
}

/** The live region of the password form. */
function passwordStatus(): HTMLElement {
  return screen.getByRole("button", { name: "Cambiar contraseña" }).closest("form")!.querySelector("[aria-live]")!;
}

/** The inline error the field points to through aria-describedby. */
function fieldError(input: HTMLElement): string {
  const ids = (input.getAttribute("aria-describedby") ?? "").split(" ").filter(Boolean);
  return ids.map((id) => document.getElementById(id)?.textContent ?? "").join(" ");
}

describe("change password on /seguridad (R6)", () => {
  test("a valid change sends both passwords, shows the success message and focuses it", async () => {
    const sent = mockApi({ "GET /auth/me": ok(me()), "POST /auth/password": noContent() });
    await renderSecurity();

    type("Contraseña actual", "la clave de antes");
    type("Contraseña nueva", "una clave nueva y larga");
    submit();

    await waitFor(() => expect(passwordStatus().textContent).toBe("Listo, cambiaste tu contraseña. Cerramos tus otras sesiones."));
    expect(sentTo(sent, "POST /auth/password").map((s) => s.body)).toEqual([{ currentPassword: "la clave de antes", newPassword: "una clave nueva y larga" }]);
    await waitFor(() => expect(document.activeElement).toBe(passwordStatus()));
    expect((screen.getByLabelText("Contraseña actual") as HTMLInputElement).value).toBe("");
    expect((screen.getByLabelText("Contraseña nueva") as HTMLInputElement).value).toBe("");
  });

  test("empty or short passwords show Spanish messages, focus the first invalid field and send nothing", async () => {
    const sent = mockApi({ "GET /auth/me": ok(me()) });
    await renderSecurity();
    const current = screen.getByLabelText("Contraseña actual");
    const next = screen.getByLabelText("Contraseña nueva");
    expect(current.closest("form")?.noValidate).toBe(true);

    submit();
    await waitFor(() => expect(document.activeElement).toBe(current));
    expect(current.getAttribute("aria-invalid")).toBe("true");
    expect(fieldError(current)).toBe("Escribí tu contraseña.");
    expect(next.getAttribute("aria-invalid")).toBe("true");
    expect(fieldError(next)).toContain("Escribí una contraseña.");

    type("Contraseña actual", "la clave de antes");
    type("Contraseña nueva", "corta");
    submit();
    await waitFor(() => expect(document.activeElement).toBe(next));
    expect(current.getAttribute("aria-invalid")).toBeNull();
    expect(next.getAttribute("aria-invalid")).toBe("true");
    expect(fieldError(next)).toContain("La contraseña tiene que tener entre 10 y 128 caracteres.");
    expect(sentTo(sent, "POST /auth/password")).toEqual([]);
  });

  test.each([
    [422, "invalid_credentials", "La contraseña actual no es correcta.", "Contraseña actual"],
    [422, "same_password", "La contraseña nueva tiene que ser distinta de la actual.", "Contraseña nueva"],
    [422, "weak_password", "La contraseña tiene que tener entre 10 y 128 caracteres.", "Contraseña nueva"],
    [429, "too_many_attempts", "Hiciste demasiados intentos. Esperá unos 15 minutos y probá de nuevo.", null],
  ])("%i %s shows its Spanish message and focuses the field it is about", async (status, code, text, label) => {
    mockApi({ "GET /auth/me": ok(me()), "POST /auth/password": fail(status, code) });
    await renderSecurity();
    type("Contraseña actual", "la clave de antes");
    type("Contraseña nueva", "una clave nueva y larga");
    submit();

    await waitFor(() => expect(passwordStatus().textContent).toBe(text));
    const target = label ? screen.getByLabelText(label) : passwordStatus();
    await waitFor(() => expect(document.activeElement).toBe(target));
    if (label) expect(target.getAttribute("aria-invalid")).toBe("true");
    // Nothing is cleared, so the person can fix and retry.
    expect((screen.getByLabelText("Contraseña nueva") as HTMLInputElement).value).toBe("una clave nueva y larga");
  });

  test("/seguridad still shows the two-factor status", async () => {
    mockApi({ "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })) });
    await renderSecurity();
    expect(screen.getByRole("heading", { level: 2, name: "Verificación en dos pasos" })).toBeTruthy();
    expect(screen.getByText("Está activada. Cada vez que ingresás te pedimos el código de tu app.")).toBeTruthy();
  });
});
