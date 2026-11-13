import { useEffect, useRef, useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { homeFor, useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { Field, FormStatus, PasswordField, SubmitButton, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { takeFragmentToken } from "../lib/fragment.ts";
import { Link, Redirect } from "../lib/router.tsx";

const LINK_SENT = "Si el mail tiene una cuenta, te enviamos un enlace para ingresar. Vence en 15 minutos.";

export function SignIn() {
  useDocumentTitle("Ingresar");
  const { session, afterSignIn } = useSession();
  // Taken once: the token was already removed from the address bar by App.
  const [linkToken] = useState(() => takeFragmentToken("/ingresar"));
  const [status, setStatus] = useState<Status>(linkToken ? { tone: "info", text: "Ingresando con tu enlace…" } : null);
  const [pending, setPending] = useState(Boolean(linkToken));
  const emailRef = useRef<HTMLInputElement>(null);
  const started = useRef(false);

  useEffect(() => {
    if (!linkToken || started.current) return;
    started.current = true; // single-use token: never send it twice (StrictMode re-runs effects)
    void call(apiClient.POST("/auth/link/consume", { body: { token: linkToken } })).then(async (result) => {
      if (result.ok) return afterSignIn();
      setStatus({ tone: "error", text: errorMessage(result.code) });
      setPending(false);
    });
  }, [linkToken, afterSignIn]);

  if (session.status === "user" && !linkToken) return <Redirect to={homeFor(session.me)} />;

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setPending(true);
    setStatus(null);
    const result = await call(
      apiClient.POST("/auth/login", {
        body: { email: String(form.get("email")), password: String(form.get("password")) },
      }),
    );
    if (result.ok) return afterSignIn();
    setStatus({ tone: "error", text: errorMessage(result.code) });
    setPending(false);
  }

  async function sendLink() {
    const email = emailRef.current;
    if (!email) return;
    if (!email.checkValidity()) {
      email.reportValidity?.();
      email.focus();
      return;
    }
    setStatus(null);
    const result = await call(apiClient.POST("/auth/link", { body: { email: email.value } }));
    setStatus(result.ok ? { tone: "ok", text: LINK_SENT } : { tone: "error", text: errorMessage(result.code) });
  }

  return (
    <AuthLayout
      footer={
        <p>
          ¿Tu escuela te dio un código? <Link to="/alta">Activá tu cuenta</Link>
        </p>
      }
    >
      <PageTitle eyebrow="Bienvenida">Ingresá</PageTitle>
      <p className="lead">Usá el mail y la contraseña de tu cuenta.</p>
      <form method="post" action="/ingresar" onSubmit={(e) => void onSubmit(e)} aria-busy={pending || undefined}>
        <Field
          ref={emailRef}
          label="Mail"
          name="email"
          type="email"
          autoComplete="username"
          spellCheck={false}
          enterKeyHint="next"
          maxLength={254}
          required
        />
        <PasswordField
          label="Contraseña"
          name="password"
          autoComplete="current-password"
          enterKeyHint="done"
          maxLength={128}
          required
        />
        <FormStatus status={status} />
        <SubmitButton pending={pending}>Ingresar</SubmitButton>
      </form>
      <div className="auth__links">
        <button type="button" className="text-button" onClick={() => void sendLink()}>
          Enviarme un enlace para ingresar
        </button>
        <Link to="/restablecer">Olvidé mi contraseña</Link>
      </div>
    </AuthLayout>
  );
}
