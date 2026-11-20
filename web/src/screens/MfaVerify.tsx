import { useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { checks, Field, FormStatus, SubmitButton, useFormErrors, type FieldState, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { SignOutButton } from "../components/SignOut.tsx";

/** One input for the 6-digit TOTP code (never split into boxes; keeps autofill and paste). */
export function OtpField(field: FieldState) {
  return (
    <Field
      {...field}
      label="Código de 6 dígitos"
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
  return <SignOutButton className="text-button">Cerrar sesión</SignOutButton>;
}

/** /seguridad/verificar: second step of sign-in for sessions with a confirmed TOTP. */
export function MfaVerify({ onComplete, onBack }: { onComplete?: () => Promise<void>; onBack?: () => void } = {}) {
  useDocumentTitle("Verificación en dos pasos");
  const { afterSignIn } = useSession();
  const [useRecovery, setUseRecovery] = useState(false);
  const [status, setStatus] = useState<Status>(null);
  const [pending, setPending] = useState(false);
  const form = useFormErrors();

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!form.validate(useRecovery ? { recoveryCode: checks.recoveryCode } : { code: checks.otp })) return setStatus(null);
    const data = new FormData(event.currentTarget);
    const body = useRecovery
      ? { recoveryCode: String(data.get("recoveryCode")) }
      : { code: String(data.get("code")).trim() };
    setPending(true);
    setStatus(null);
    const result = await call(apiClient.POST("/auth/mfa/verify", { body }));
    if (result.ok) return onComplete ? onComplete() : afterSignIn();
    setStatus({ tone: "error", text: errorMessage(result.code, useRecovery ? "mfa-recovery" : "mfa") });
    setPending(false);
    form.failed(result.code);
  }

  return (
    <AuthLayout footer={<SignOutLink />}>
      <PageTitle eyebrow="Verificación en dos pasos">Verificá que sos vos</PageTitle>
      <p className="lead">
        {useRecovery
          ? "Escribí uno de los códigos de recuperación que guardaste al activar la verificación."
          : "Abrí tu app de autenticación y escribí el código de Centro de Estudiantes."}
      </p>
      <form method="post" action="/seguridad/verificar" {...form.formProps} onSubmit={(e) => void onSubmit(e)}>
        {useRecovery ? (
          <Field
            key="recovery"
            {...form.field("recoveryCode")}
            label="Código de recuperación"
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
          <OtpField key="totp" {...form.field("code")} />
        )}
        <FormStatus status={status} {...form.statusProps} />
        <SubmitButton pending={pending}>Verificar</SubmitButton>
      </form>
      <div className="auth__links">
        {onBack && <button type="button" className="text-button" onClick={onBack}>{"Volver a las opciones"}</button>}
        <button
          type="button"
          className="text-button"
          onClick={() => {
            setUseRecovery((r) => !r);
            setStatus(null);
            form.clear();
          }}
        >
          {useRecovery ? "Usar la app de autenticación" : "Usar un código de recuperación"}
        </button>
      </div>
    </AuthLayout>
  );
}
