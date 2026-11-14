import { useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { checks, Field, FormStatus, PasswordField, SubmitButton, useFormErrors, type Status } from "../components/Form.tsx";
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
  const form = useFormErrors();

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const valid = form.validate(
      signedIn
        ? { code: checks.accessCode }
        : { code: checks.accessCode, email: checks.email, password: checks.newPassword },
    );
    if (!valid) return setStatus(null);
    const data = new FormData(event.currentTarget);
    setPending(true);
    setStatus(null);
    const result = signedIn
      ? await call(apiClient.POST("/auth/redeem", { body: { code } }))
      : await call(
          apiClient.POST("/auth/claim", {
            body: { code, email: String(data.get("email")), password: String(data.get("password")) },
          }),
        );
    if (result.ok) return afterSignIn();
    setStatus({ tone: "error", text: errorMessage(result.code, "access-code") });
    setPending(false);
    form.failed(result.code);
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
      <form method="post" action="/alta" {...form.formProps} onSubmit={(e) => void onSubmit(e)} aria-busy={pending || undefined}>
        <Field
          {...form.field("code")}
          label="Código de acceso"
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
              hint="Entre 10 y 128 caracteres. Tu navegador puede sugerirte una segura."
              autoComplete="new-password"
              enterKeyHint="done"
              minLength={10}
              maxLength={128}
              required
            />
          </>
        )}
        <FormStatus status={status} {...form.statusProps} />
        <SubmitButton pending={pending}>{signedIn ? "Canjear código" : "Crear cuenta"}</SubmitButton>
      </form>
    </AuthLayout>
  );
}
