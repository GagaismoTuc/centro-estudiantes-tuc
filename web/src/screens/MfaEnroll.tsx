import { useEffect, useRef, useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { CopyButton } from "../components/CopyButton.tsx";
import { FormStatus, SubmitButton, type Status } from "../components/Form.tsx";
import { Icon } from "../components/Icon.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
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
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true; // a second call would replace the secret already on screen
    void call(apiClient.POST("/auth/mfa/enrollment")).then((result) => {
      if (result.ok) setEnrollment(result.data);
      else setStatus({ tone: "error", text: errorMessage(result.code) });
    });
  }, []);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const code = String(new FormData(event.currentTarget).get("code"));
    setPending(true);
    setStatus(null);
    const result = await call(apiClient.POST("/auth/mfa/enrollment/confirm", { body: { code } }));
    setPending(false);
    if (result.ok) onConfirmed(result.data.recoveryCodes);
    else setStatus({ tone: "error", text: errorMessage(result.code, "mfa") });
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
            <p>Agregá esta clave en tu app.</p>
            <div className="secret">
              <p className="eyebrow">Clave para tu app</p>
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
            {/* QR codes are out of scope for now: manual entry and the otpauth: link cover phones. */}
            <p className="muted small">Si la app te pide escanear un QR, elegí «Ingresar clave manualmente» y pegá la clave.</p>
          </li>
          <li>
            <p>Escribí el código que muestra la app.</p>
            <form method="post" action="/seguridad/activar" onSubmit={(e) => void onSubmit(e)}>
              <OtpField />
              <FormStatus status={status} />
              <SubmitButton pending={pending}>Activar</SubmitButton>
            </form>
          </li>
        </ol>
      )}
      {!enrollment && <FormStatus status={status} />}
    </AuthLayout>
  );
}

function RecoveryCodes({ codes }: { codes: string[] }) {
  useDocumentTitle("Códigos de recuperación");
  const { afterSignIn } = useSession();
  const [saved, setSaved] = useState(false);
  const [status, setStatus] = useState<Status>(null);
  const [pending, setPending] = useState(false);
  const notSaved: Status = { tone: "error", text: "Marcá la casilla cuando hayas guardado los códigos." };

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!saved) return setStatus(notSaved);
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
      <form method="post" action="/seguridad/activar" onSubmit={(e) => void onSubmit(e)}>
        <div className="check">
          <input
            id="codes-saved"
            type="checkbox"
            required
            checked={saved}
            onChange={(e) => {
              setSaved(e.target.checked);
              if (e.target.checked) setStatus(null);
            }}
            onInvalid={() => setStatus(notSaved)}
          />
          <label htmlFor="codes-saved">Ya los guardé en un lugar seguro</label>
        </div>
        <FormStatus status={status} />
        <SubmitButton pending={pending}>Continuar</SubmitButton>
      </form>
    </AuthLayout>
  );
}
