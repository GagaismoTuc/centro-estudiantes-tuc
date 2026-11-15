import { useEffect, useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { useMe } from "../auth/session.tsx";
import { checks, FormStatus, PasswordField, SubmitButton, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";

/** /seguridad: two-factor status of the account and the password change. */
export function Security() {
  useDocumentTitle("Seguridad");
  const me = useMe();
  return (
    <>
      <header className="page-header">
        <PageTitle eyebrow="Cuenta">Seguridad</PageTitle>
      </header>
      <section className="panel" aria-labelledby="mfa-title">
        <h2 id="mfa-title">Verificación en dos pasos</h2>
        {me.mfa === "satisfied" && <p>Está activada. Cada vez que ingresás te pedimos el código de tu app.</p>}
        {me.mfa === "not_required" && <p>Tu rol no la necesita. Tu cuenta se protege con tu contraseña y tu mail.</p>}
        <p className="muted">
          Si perdés el celular y los códigos de recuperación, pedile a quien administra la plataforma que la restablezca.
        </p>
      </section>
      <ChangePassword />
    </>
  );
}

function ChangePassword() {
  const [status, setStatus] = useState<Status>(null);
  const [pending, setPending] = useState(false);
  // Bumped on every success: the effect focuses the message after the render that shows it.
  const [saved, setSaved] = useState(0);
  const form = useFormErrors();

  useEffect(() => {
    if (saved) document.getElementById(form.statusProps.id)?.focus();
  }, [saved, form.statusProps.id]);

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!form.validate({ currentPassword: checks.password, newPassword: checks.newPassword })) return setStatus(null);
    const element = event.currentTarget;
    const data = new FormData(element);
    const body = { currentPassword: String(data.get("currentPassword")), newPassword: String(data.get("newPassword")) };
    setPending(true);
    setStatus(null);
    const result = await call(apiClient.POST("/auth/password", { body }));
    setPending(false);
    if (result.ok) {
      element.reset();
      form.clear();
      setStatus({ tone: "ok", text: "Listo, cambiaste tu contraseña. Cerramos tus otras sesiones." });
      setSaved((n) => n + 1);
      return;
    }
    setStatus({ tone: "error", text: errorMessage(result.code, "change-password") });
    form.failed(result.code);
  }

  return (
    <section className="panel" aria-labelledby="password-title">
      <h2 id="password-title">Contraseña</h2>
      <p className="muted">Al cambiarla se cierran tus sesiones en otros dispositivos.</p>
      <form method="post" action="/seguridad" {...form.formProps} onSubmit={(e) => void save(e)}>
        <PasswordField
          {...form.field("currentPassword")}
          label="Contraseña actual"
          autoComplete="current-password"
          enterKeyHint="next"
          required
        />
        <PasswordField
          {...form.field("newPassword")}
          label="Contraseña nueva"
          hint="Entre 10 y 128 caracteres. Tu navegador puede sugerirte una segura."
          autoComplete="new-password"
          enterKeyHint="done"
          minLength={10}
          maxLength={128}
          required
        />
        <FormStatus status={status} {...form.statusProps} />
        <SubmitButton pending={pending}>Cambiar contraseña</SubmitButton>
      </form>
    </section>
  );
}

export function NotFound() {
  useDocumentTitle("Página no encontrada");
  return (
    <>
      <header className="page-header">
        <PageTitle>No encontramos esta página</PageTitle>
      </header>
      <p>
        <Link to="/">Volver al inicio</Link>
      </p>
    </>
  );
}
