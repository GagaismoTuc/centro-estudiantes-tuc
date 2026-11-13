import { useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { Field, FormStatus, PasswordField, SubmitButton, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { takeFragmentToken } from "../lib/fragment.ts";
import { Link } from "../lib/router.tsx";

const RESET_SENT =
  "Si el mail tiene una cuenta, te enviamos un enlace para elegir una contraseña nueva. Vence en 30 minutos.";

/** /restablecer asks for the reset link; /restablecer#token=… sets the new password. */
export function ResetPassword() {
  const { session, refresh } = useSession();
  const [token, setToken] = useState(() => takeFragmentToken("/restablecer"));
  useDocumentTitle(token ? "Contraseña nueva" : "Restablecer contraseña");
  const [status, setStatus] = useState<Status>(null);
  const [pending, setPending] = useState(false);
  const [done, setDone] = useState(false);

  async function requestLink(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const email = String(new FormData(event.currentTarget).get("email"));
    setPending(true);
    setStatus(null);
    const result = await call(apiClient.POST("/auth/password/reset-request", { body: { email } }));
    setStatus(result.ok ? { tone: "ok", text: RESET_SENT } : { tone: "error", text: errorMessage(result.code) });
    setPending(false);
  }

  async function setPassword(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!token) return;
    const password = String(new FormData(event.currentTarget).get("password"));
    setPending(true);
    setStatus(null);
    const result = await call(apiClient.POST("/auth/password/reset", { body: { token, password } }));
    setPending(false);
    if (result.ok) {
      setDone(true);
      // The reset signed out every session, possibly this one too.
      if (session.status === "user") void refresh();
      setStatus({ tone: "ok", text: "Listo, ya podés ingresar con tu contraseña nueva." });
      return;
    }
    // weak_password leaves the token usable: keep the form for another try.
    setStatus({ tone: "error", text: errorMessage(result.code, "reset") });
  }

  if (!token) {
    return (
      <AuthLayout footer={<Link to="/ingresar">Volver a ingresar</Link>}>
        <PageTitle>Restablecé tu contraseña</PageTitle>
        <p className="lead">Te mandamos un enlace para elegir una contraseña nueva.</p>
        <form method="post" action="/restablecer" onSubmit={(e) => void requestLink(e)}>
          <Field
            label="Mail"
            name="email"
            type="email"
            autoComplete="username"
            spellCheck={false}
            enterKeyHint="send"
            maxLength={254}
            required
          />
          <FormStatus status={status} />
          <SubmitButton pending={pending}>Enviarme el enlace</SubmitButton>
        </form>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <PageTitle>Elegí una contraseña nueva</PageTitle>
      <p className="lead">Al guardarla se cierran todas tus sesiones abiertas.</p>
      <form method="post" action="/restablecer" onSubmit={(e) => void setPassword(e)}>
        {!done && (
          <PasswordField
            label="Contraseña nueva"
            name="password"
            hint="Entre 10 y 128 caracteres. Tu navegador puede sugerirte una segura."
            autoComplete="new-password"
            enterKeyHint="done"
            minLength={10}
            maxLength={128}
            required
          />
        )}
        <FormStatus status={status} />
        {done ? (
          <Link className="button button--primary button--block" to="/ingresar">
            Ingresar
          </Link>
        ) : (
          <SubmitButton pending={pending}>Guardar contraseña</SubmitButton>
        )}
      </form>
      {!done && (
        <div className="auth__links">
          <button type="button" className="text-button" onClick={() => setToken(null)}>
            Pedir un enlace nuevo
          </button>
        </div>
      )}
    </AuthLayout>
  );
}
