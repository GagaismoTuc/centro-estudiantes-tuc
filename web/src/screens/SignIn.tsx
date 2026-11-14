import { useEffect, useRef, useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { homeFor, useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { checks, Field, FormStatus, PasswordField, SubmitButton, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { useFragmentToken } from "../lib/fragment.ts";
import { destinationFor, readNext, saveLinkNext, takeLinkNext } from "../lib/next.ts";
import { Link, Redirect } from "../lib/router.tsx";

const LINK_SENT = "Si el mail tiene una cuenta, te enviamos un enlace para ingresar. Vence en 15 minutos.";

export function SignIn() {
  useDocumentTitle("Ingresar");
  const { session, afterSignIn } = useSession();
  // The token was already removed from the address bar; a newer link replaces it.
  const [linkToken, setLinkToken] = useFragmentToken("/ingresar");
  // A link opened while signed in would replace that session: only after the user agrees.
  const [linkApproved, setLinkApproved] = useState(session.status !== "user");
  const [status, setStatus] = useState<Status>(linkToken ? { tone: "info", text: "Ingresando con tu enlace…" } : null);
  const [pending, setPending] = useState(Boolean(linkToken));
  const form = useFormErrors();
  const started = useRef<string | null>(null);

  useEffect(() => {
    if (!linkToken || !linkApproved || started.current === linkToken) return;
    started.current = linkToken; // single-use token: never send it twice (StrictMode re-runs effects)
    setStatus({ tone: "info", text: "Ingresando con tu enlace…" });
    setPending(true);
    void call(apiClient.POST("/auth/link/consume", { body: { token: linkToken } })).then(async (result) => {
      if (result.ok) {
        const storedNext = takeLinkNext();
        const urlNext = readNext(window.location.search);
        return afterSignIn(urlNext !== "/" ? urlNext : storedNext);
      }
      setStatus({ tone: "error", text: errorMessage(result.code) });
      setPending(false);
    });
  }, [linkToken, linkApproved, afterSignIn]);

  if (session.status === "user" && !linkToken) {
    return <Redirect to={destinationFor(homeFor(session.me), readNext(window.location.search))} />;
  }
  if (session.status === "user" && !linkApproved) {
    return (
      <AuthLayout>
        <PageTitle eyebrow="Enlace para ingresar">¿Cambiar de cuenta?</PageTitle>
        <p className="lead">
          {`Ya ingresaste como ${session.me.email}. Si seguís con el enlace, se cierra esa sesión y entrás con la cuenta del enlace.`}
        </p>
        <button type="button" className="button button--primary button--block" onClick={() => setLinkApproved(true)}>
          Ingresar con el enlace
        </button>
        <button type="button" className="button button--secondary button--block" onClick={() => setLinkToken(null)}>
          Seguir como {session.me.email}
        </button>
      </AuthLayout>
    );
  }

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!form.validate({ email: checks.email, password: checks.password })) return setStatus(null);
    const data = new FormData(event.currentTarget);
    setPending(true);
    setStatus(null);
    const result = await call(
      apiClient.POST("/auth/login", {
        body: { email: String(data.get("email")), password: String(data.get("password")) },
      }),
    );
    if (result.ok) return afterSignIn();
    setStatus({ tone: "error", text: errorMessage(result.code) });
    setPending(false);
    form.failed(result.code);
  }

  async function sendLink() {
    setStatus(null);
    if (!form.validate({ email: checks.email })) return;
    const email = form.value("email").trim();
    const result = await call(apiClient.POST("/auth/link", { body: { email } }));
    if (result.ok) {
      saveLinkNext(readNext(window.location.search));
      setStatus({ tone: "ok", text: LINK_SENT });
      return;
    }
    setStatus({ tone: "error", text: errorMessage(result.code) });
    form.failed(result.code);
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
      <form method="post" action="/ingresar" {...form.formProps} onSubmit={(e) => void onSubmit(e)} aria-busy={pending || undefined}>
        <Field
          {...form.field("email")}
          label="Mail"
          type="email"
          autoComplete="username"
          spellCheck={false}
          enterKeyHint="next"
          maxLength={254}
          required
        />
        <PasswordField
          {...form.field("password")}
          label="Contraseña"
          autoComplete="current-password"
          enterKeyHint="done"
          maxLength={128}
          required
        />
        <FormStatus status={status} {...form.statusProps} />
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
