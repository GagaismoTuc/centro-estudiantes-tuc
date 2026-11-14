import { useState, type ReactNode } from "react";
import { errorMessage } from "../api/errors.ts";
import { useSession } from "../auth/session.tsx";
import { useLeaveConfirm } from "../lib/router.tsx";
import { FormStatus, type Status } from "./Form.tsx";

/**
 * Signs out, or says why it could not: while the server keeps the session the user is
 * still signed in, so we never pretend otherwise.
 */
export function SignOutButton({ className, children }: { className: string; children: ReactNode }) {
  const { signOut } = useSession();
  const [status, setStatus] = useState<Status>(null);
  const { confirmLeave, dialog } = useLeaveConfirm();

  async function signOutNow() {
    setStatus(null);
    const code = await signOut();
    if (code) setStatus({ tone: "error", text: `No se cerró la sesión. ${errorMessage(code)}` });
  }

  return (
    <>
      <button type="button" className={className} onClick={() => confirmLeave(() => void signOutNow())}>
        {children}
      </button>
      {dialog}
      <FormStatus status={status} />
    </>
  );
}
