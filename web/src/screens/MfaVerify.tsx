import { useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { Field, FormStatus, SubmitButton, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";

/** One input for the 6-digit TOTP code (never split into boxes; keeps autofill and paste). */
export function OtpField() {
  return (
    <Field
      label="Código de 6 dígitos"
      name="code"
      hint="Lo muestra tu app y cambia cada 30 segundos."
      className="input--code"
      type="text"
      inputMode="numeric"
      autoComplete="one-time-code"
      pattern="\d{6}"
      maxLength={6}
      enterKeyHint="done"
      required
    />
  );
}

export function SignOutLink() {
  const { signOut } = useSession();
  return (
    <button type="button" className="text-button" onClick={() => void signOut()}>
      Cerrar sesión
    </button>
  );
}

/** /seguridad/verificar: second step of sign-in for sessions with a confirmed TOTP. */
export function MfaVerify() {
  useDocumentTitle("Verificación en dos pasos");
  const { afterSignIn } = useSession();
  const [useRecovery, setUseRecovery] = useState(false);
  const [status, setStatus] = useState<Status>(null);
  const [pending, setPending] = useState(false);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const body = useRecovery ? { recoveryCode: String(form.get("recoveryCode")) } : { code: String(form.get("code")) };
    setPending(true);
    setStatus(null);
    const result = await call(apiClient.POST("/auth/mfa/verify", { body }));
    if (result.ok) return afterSignIn();
    setStatus({ tone: "error", text: errorMessage(result.code, "mfa") });
    setPending(false);
  }

  return (
    <AuthLayout footer={<SignOutLink />}>
      <PageTitle eyebrow="Verificación en dos pasos">Verificá que sos vos</PageTitle>
      <p className="lead">
        {useRecovery
          ? "Escribí uno de los códigos de recuperación que guardaste al activar la verificación."
          : "Abrí tu app de autenticación y escribí el código de Centro de Estudiantes."}
      </p>
      <form method="post" action="/seguridad/verificar" onSubmit={(e) => void onSubmit(e)}>
        {useRecovery ? (
          <Field
            key="recovery"
            label="Código de recuperación"
            name="recoveryCode"
            hint="Tiene la forma XXXXX-XXXXX. Cada uno sirve una sola vez."
            className="input--code"
            autoComplete="off"
            autoCapitalize="characters"
            autoCorrect="off"
            spellCheck={false}
            maxLength={16}
            enterKeyHint="done"
            required
          />
        ) : (
          <OtpField key="totp" />
        )}
        <FormStatus status={status} />
        <SubmitButton pending={pending}>Verificar</SubmitButton>
      </form>
      <div className="auth__links">
        <button
          type="button"
          className="text-button"
          onClick={() => {
            setUseRecovery((r) => !r);
            setStatus(null);
          }}
        >
          {useRecovery ? "Usar la app de autenticación" : "Usar un código de recuperación"}
        </button>
      </div>
    </AuthLayout>
  );
}
