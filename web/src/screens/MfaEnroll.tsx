import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { CopyButton } from "../components/CopyButton.tsx";
import { checks, FieldError, FormStatus, SubmitButton, useFormErrors, type Status } from "../components/Form.tsx";
import { Icon } from "../components/Icon.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { QrCode } from "../components/QrCode.tsx";
import { groupSecret } from "../lib/accessCode.ts";
import { OtpField, SignOutLink } from "./MfaVerify.tsx";

type Enrollment = components["schemas"]["MfaEnrollment"];

/** /seguridad/activar: TOTP enrollment for school admins and officers. */
export function MfaEnroll() {
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  return recoveryCodes ? <RecoveryCodes codes={recoveryCodes} /> : <Setup onConfirmed={setRecoveryCodes} />;
}

function Setup({ onConfirmed }: { onConfirmed: (codes: string[]) => void }) {
  useDocumentTitle("Activar verificación en dos pasos");
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const started = useRef(false);
  const form = useFormErrors();

  const start = useCallback(() => {
    setFailed(false);
    setStatus(null);
    void call(apiClient.POST("/auth/mfa/enrollment")).then((result) => {
      if (result.ok) return setEnrollment(result.data);
      setStatus({ tone: "error", text: errorMessage(result.code) });
      setFailed(true);
    });
  }, []);

  useEffect(() => {
    if (started.current) return;
    started.current = true; // a second call would replace the secret already on screen
    start();
  }, [start]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!form.validate({ code: checks.otp })) return setStatus(null);
    const code = String(new FormData(event.currentTarget).get("code")).trim();
    setPending(true);
    setStatus(null);
    const result = await call(apiClient.POST("/auth/mfa/enrollment/confirm", { body: { code } }));
    setPending(false);
    if (result.ok) onConfirmed(result.data.recoveryCodes);
    else {
      setStatus({ tone: "error", text: errorMessage(result.code, "mfa") });
      form.failed(result.code);
    }
  }

  return (
    <AuthLayout footer={<SignOutLink />}>
      <PageTitle eyebrow="Seguridad">Activá la verificación en dos pasos</PageTitle>
      <p className="lead">
        Por tu rol, además de la contraseña vas a usar un código que genera una app en tu celular (Google Authenticator,
        Microsoft Authenticator, Aegis u otra).
      </p>
      {enrollment && (
        <ol className="steps">
          <li>
            <p>Escaneá este código QR con tu app.</p>
            <QrCode text={enrollment.otpauthUri} label="Código QR para tu app de autenticación" />
            <div className="secret">
              <p className="eyebrow">Si no podés escanearlo, usá esta clave</p>
              <code className="secret__value">
                {groupSecret(enrollment.secret)}
              </code>
              <div className="secret__actions">
                <CopyButton text={enrollment.secret} label="Copiar clave" />
                <a className="button button--secondary" href={enrollment.otpauthUri}>
                  Abrir en la app de autenticación
                </a>
              </div>
            </div>
            <p className="muted small">En la app elegí la opción para ingresar una clave y pegá esta.</p>
          </li>
          <li>
            <p>Escribí el código que muestra la app.</p>
            <form method="post" action="/seguridad/activar" {...form.formProps} onSubmit={(e) => void onSubmit(e)}>
              <OtpField {...form.field("code")} />
              <FormStatus status={status} {...form.statusProps} />
              <SubmitButton pending={pending}>Activar</SubmitButton>
            </form>
          </li>
        </ol>
      )}
      {!enrollment && <FormStatus status={status} />}
      {failed && (
        <button type="button" className="button button--primary button--block" onClick={start}>
          Reintentar
        </button>
      )}
    </AuthLayout>
  );
}

function RecoveryCodes({ codes }: { codes: string[] }) {
  useDocumentTitle("Códigos de recuperación");
  const { afterSignIn } = useSession();
  const [saved, setSaved] = useState(false);
  const [pending, setPending] = useState(false);
  const form = useFormErrors();
  const savedField = form.field("saved");

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!form.validate({ saved: (v) => (v ? null : "Marcá la casilla cuando hayas guardado los códigos.") })) return;
    setPending(true);
    await afterSignIn();
  }

  return (
    <AuthLayout>
      <PageTitle eyebrow="Seguridad">Guardá tus códigos de recuperación</PageTitle>
      <p className="lead">
        Si perdés el celular, cada código te deja ingresar una vez. Solo los vas a ver ahora: copialos, imprimilos o
        anotalos en un lugar seguro.
      </p>
      <div className="secret print-area">
        <p className="eyebrow" id="recovery-label">
          Códigos de recuperación
        </p>
        <ul className="codes" aria-labelledby="recovery-label">
          {codes.map((code) => (
            <li key={code}>{code}</li>
          ))}
        </ul>
        <div className="secret__actions">
          <CopyButton text={codes.join("\n")} label="Copiar códigos" />
          <button type="button" className="button button--secondary" onClick={() => window.print()}>
            <Icon name="print" />
            Imprimir
          </button>
        </div>
      </div>
      <form method="post" action="/seguridad/activar" {...form.formProps} onSubmit={(e) => void onSubmit(e)}>
        <div className="check">
          <input
            id="codes-saved"
            name="saved"
            type="checkbox"
            required
            checked={saved}
            aria-invalid={savedField.error ? true : undefined}
            aria-describedby={savedField.error ? "codes-saved-error" : undefined}
            onChange={(e) => setSaved(e.target.checked)}
          />
          <label htmlFor="codes-saved">Ya los guardé en un lugar seguro</label>
        </div>
        {savedField.error && <FieldError id="codes-saved-error">{savedField.error}</FieldError>}
        <SubmitButton pending={pending}>Continuar</SubmitButton>
      </form>
    </AuthLayout>
  );
}
