import { useEffect, useRef, useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { homeFor, useSession } from "../auth/session.tsx";
import { AuthLayout } from "../components/AuthLayout.tsx";
import { checks, FormStatus, PasswordField, SubmitButton, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { useFragmentToken } from "../lib/fragment.ts";
import { destinationFor, withNext } from "../lib/next.ts";
import { Link, Redirect, navigate } from "../lib/router.tsx";
import { MfaEnroll } from "./MfaEnroll.tsx";
import { MfaVerify } from "./MfaVerify.tsx";

// Kept only in memory while the invitee signs in and completes MFA, never in a URL or storage.
let pendingInvitation: string | null = null;

export function Invitation() {
  useDocumentTitle("Aceptar invitaci\u00f3n");
  const { session, afterSignIn, signOut, refresh } = useSession();
  const [captured] = useFragmentToken("/invitacion");
  const [token, setToken] = useState(() => captured ?? pendingInvitation);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const [mfa, setMfa] = useState<"choose" | "enroll" | "verify" | null>(null);
  const form = useFormErrors();
  const choiceRef = useRef<HTMLParagraphElement>(null);
  const signedIn = session.status === "user";
  const redirecting = signedIn && homeFor(session.me) !== "/";
  useEffect(() => { if (captured) setToken(captured); if (!redirecting) pendingInvitation = null; }, [captured, redirecting]);
  useEffect(() => { if (mfa === "choose") choiceRef.current?.focus(); }, [mfa]);
  if (redirecting) {
    pendingInvitation = token;
    return <Redirect to={destinationFor(homeFor(session.me), "/invitacion")} />;
  }

  if (mfa === "enroll" || mfa === "verify") {
    const complete = async () => { await refresh(); setMfa(null); };
    return mfa === "enroll" ? <MfaEnroll onComplete={complete} onBack={() => setMfa("choose")} /> : <MfaVerify onComplete={complete} onBack={() => setMfa("choose")} />;
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!token || busy) return;
    if (!signedIn && !form.validate({ password: checks.newPassword })) return;
    const password = signedIn ? undefined : form.value("password");
    setBusy(true); setStatus(null);
    const result = await call(apiClient.POST("/auth/invitations/accept", { body: { token, ...(password === undefined ? {} : { password }) } }));
    if (result.ok) {
      pendingInvitation = null;
      setToken(null);
      await afterSignIn();
      return;
    }
    setBusy(false);
    if (result.code === "mfa_required") { setMfa("choose"); return; }
    if (result.code === "invalid_token") pendingInvitation = null;
    if (result.code === "login_required") setStatus({ tone: "error", text: "Esta invitaci\u00f3n es para una cuenta existente. Ingres\u00e1 con esa cuenta y complet\u00e1 la verificaci\u00f3n en dos pasos." });
    else setStatus({ tone: "error", text: errorMessage(result.code) });
    form.failed(result.code);
  }

  return <AuthLayout>
    <PageTitle>{"Acept\u00e1 la invitaci\u00f3n"}</PageTitle>
    {!token ? <p>{"Este enlace est\u00e1 incompleto. Abrilo de nuevo desde el mail."}</p> : <>
      {mfa === "choose" ? <><p role="alert" ref={choiceRef} tabIndex={-1}>{"Para aceptar la invitaci\u00f3n, complet\u00e1 la verificaci\u00f3n en dos pasos."}</p><div className="panel__actions"><button type="button" className="button button--primary" onClick={() => setMfa("verify")}>{"Ya tengo verificaci\u00f3n en dos pasos"}</button><button type="button" className="button button--secondary" onClick={() => setMfa("enroll")}>{"Activar verificaci\u00f3n en dos pasos"}</button></div></> : <>
      <p className="lead">{signedIn ? "Sum\u00e1 esta escuela a tu cuenta." : "Eleg\u00ed una contrase\u00f1a para crear tu cuenta."}</p>
      <form {...form.formProps} onSubmit={(event) => void submit(event)} aria-busy={busy || undefined}>
        {!signedIn && <PasswordField {...form.field("password")} label={"Eleg\u00ed tu contrase\u00f1a"} autoComplete="new-password" minLength={10} maxLength={128} required />}
        <FormStatus status={status} {...form.statusProps} />
        <SubmitButton pending={busy}>{"Aceptar invitaci\u00f3n"}</SubmitButton>
      </form>
      {!signedIn && <p><Link to={withNext("/ingresar", "/invitacion")} onClick={() => { pendingInvitation = token; }}>{"Ya ten\u00e9s cuenta: ingres\u00e1"}</Link></p>}
      {signedIn && status?.tone === "error" && <button type="button" className="text-button" onClick={() => { void signOut().then((error) => { if (!error) { pendingInvitation = token; navigate(withNext("/ingresar", "/invitacion"), { replace: true }); } }); }}>Ingresar con otra cuenta</button>}
      </>}
    </>}
  </AuthLayout>;
}
