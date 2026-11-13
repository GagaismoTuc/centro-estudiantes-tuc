import { useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { Field, FormStatus, PasswordField, SubmitButton, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { formatAccessCode } from "../lib/accessCode.ts";
import { Link } from "../lib/router.tsx";

/**
 * /alta: signed out, the access code creates the account (claim); signed in, the same
 * code adds the school to the current account (redeem).
 */
export function Claim() {
  const { session, afterSignIn } = useSession();
  const signedIn = session.status === "user";
  useDocumentTitle(signedIn ? "Canjear código" : "Activar cuenta");
  const [code, setCode] = useState("");
  const [status, setStatus] = useState<Status>(null);
  const [pending, setPending] = useState(false);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setPending(true);
    setStatus(null);
    const result = signedIn
      ? await call(apiClient.POST("/auth/redeem", { body: { code } }))
      : await call(
          apiClient.POST("/auth/claim", {
            body: { code, email: String(form.get("email")), password: String(form.get("password")) },
          }),
        );
    if (result.ok) return afterSignIn();
    setStatus({ tone: "error", text: errorMessage(result.code, "access-code") });
    setPending(false);
  }

  return (
    <AuthLayout
      footer={
        signedIn ? (
          <p>
            <Link to="/">Volver al inicio</Link>
          </p>
        ) : (
          <p>
            ¿Ya tenés cuenta? <Link to="/ingresar">Ingresá</Link> y después canjeá el código acá.
          </p>
        )
      }
    >
      <PageTitle eyebrow="Código de tu escuela">{signedIn ? "Sumá tu escuela" : "Activá tu cuenta"}</PageTitle>
      <p className="lead">
        {signedIn
          ? "Escribí el código que te dio la escuela para sumarla a tu cuenta."
          : "Con el código que te dio la escuela creás tu cuenta. Después ingresás con tu mail."}
      </p>
      <form method="post" action="/alta" onSubmit={(e) => void onSubmit(e)} aria-busy={pending || undefined}>
        <Field
          label="Código de acceso"
          name="code"
          hint="Tiene 12 letras y números. Podés escribirlo con o sin guiones."
          className="input--code"
          value={code}
          onChange={(e) => setCode(formatAccessCode(e.target.value))}
          autoComplete="off"
          autoCapitalize="characters"
          autoCorrect="off"
          spellCheck={false}
          maxLength={14}
          pattern="[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}"
          title="12 letras y números, por ejemplo ABCD-EFGH-JKMN"
          enterKeyHint={signedIn ? "done" : "next"}
          required
        />
        {!signedIn && (
          <>
            <Field
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
              hint="Entre 10 y 128 caracteres. Tu navegador puede sugerirte una segura."
              autoComplete="new-password"
              enterKeyHint="done"
              minLength={10}
              maxLength={128}
              required
            />
          </>
        )}
        <FormStatus status={status} />
        <SubmitButton pending={pending}>{signedIn ? "Canjear código" : "Crear cuenta"}</SubmitButton>
      </form>
    </AuthLayout>
  );
}
