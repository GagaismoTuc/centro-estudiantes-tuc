import { useEffect, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { FormStatus, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { useFragmentToken } from "../lib/fragment.ts";
import { Link } from "../lib/router.tsx";

const NO_TOKEN = "Este enlace está incompleto. Abrilo de nuevo desde el mail.";

/** /verificar#token=…: confirms the email address from the emailed link. */
export function VerifyEmail() {
  useDocumentTitle("Verificar mail");
  const { session, refresh } = useSession();
  // A newer emailed link opened on this same screen replaces the token.
  const [token] = useFragmentToken("/verificar");
  const [state, setState] = useState<"verifying" | "done" | "failed">(token ? "verifying" : "failed");
  const [status, setStatus] = useState<Status>(token ? null : { tone: "error", text: NO_TOKEN });
  const started = useRef<string | null>(null);
  const signedIn = session.status === "user";

  useEffect(() => {
    if (!token || started.current === token) return;
    started.current = token; // single-use token: never send it twice (StrictMode re-runs effects)
    setState("verifying");
    setStatus(null);
    void call(apiClient.POST("/auth/email/verify", { body: { token } })).then((result) => {
      if (result.ok) {
        setState("done");
        if (signedIn) void refresh();
        return;
      }
      setState("failed");
      setStatus({ tone: "error", text: errorMessage(result.code, "verify") });
    });
  }, [token, signedIn, refresh]);

  const title = state === "done" ? "¡Mail verificado!" : state === "verifying" ? "Verificando tu mail…" : "No pudimos verificar tu mail";
  return (
    <AuthLayout>
      <PageTitle key={state}>{title}</PageTitle>
      {state === "done" && <p className="lead">Ya podés ingresar con enlaces por mail y recuperar tu contraseña si la olvidás.</p>}
      {/* The reason first, then what to do about it. */}
      <FormStatus status={status} />
      {state === "failed" && (
        <p className="lead">
          {signedIn ? "Desde el inicio podés pedir un enlace nuevo." : "Ingresá y desde el inicio pedí un enlace nuevo."}
        </p>
      )}
      {state !== "verifying" && (
        <Link className="button button--primary button--block" to={signedIn ? "/" : "/ingresar"}>
          {signedIn ? "Ir al inicio" : "Ingresar"}
        </Link>
      )}
    </AuthLayout>
  );
}
