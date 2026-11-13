import { useEffect, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { FormStatus, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { takeFragmentToken } from "../lib/fragment.ts";
import { Link } from "../lib/router.tsx";

/** /verificar#token=…: confirms the email address from the emailed link. */
export function VerifyEmail() {
  useDocumentTitle("Verificar mail");
  const { session, refresh } = useSession();
  const [token] = useState(() => takeFragmentToken("/verificar"));
  const [state, setState] = useState<"verifying" | "done" | "failed">(token ? "verifying" : "failed");
  const [status, setStatus] = useState<Status>(token ? null : { tone: "error", text: errorMessage("invalid_token") });
  const started = useRef(false);
  const signedIn = session.status === "user";

  useEffect(() => {
    if (!token || started.current) return;
    started.current = true;
    void call(apiClient.POST("/auth/email/verify", { body: { token } })).then((result) => {
      if (result.ok) {
        setState("done");
        if (signedIn) void refresh();
        return;
      }
      setState("failed");
      setStatus({ tone: "error", text: errorMessage(result.code) });
    });
  }, [token, signedIn, refresh]);

  const title = state === "done" ? "¡Mail verificado!" : state === "verifying" ? "Verificando tu mail…" : "No pudimos verificar tu mail";
  return (
    <AuthLayout>
      <PageTitle key={state}>{title}</PageTitle>
      {state === "done" && <p className="lead">Ya podés ingresar con enlaces por mail y recuperar tu contraseña si la olvidás.</p>}
      {state === "failed" && <p className="lead">Desde el inicio podés pedir un enlace nuevo.</p>}
      <FormStatus status={status} />
      {state !== "verifying" && (
        <Link className="button button--primary button--block" to={signedIn ? "/" : "/ingresar"}>
          {signedIn ? "Ir al inicio" : "Ingresar"}
        </Link>
      )}
    </AuthLayout>
  );
}
