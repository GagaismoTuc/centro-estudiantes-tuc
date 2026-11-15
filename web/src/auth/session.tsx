import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import type { components } from "../api/schema.gen.ts";
import { destinationFor, readNext, safeNext } from "../lib/next.ts";
import { navigate } from "../lib/router.tsx";

export type Me = components["schemas"]["Me"];
export type Role = components["schemas"]["Role"];

export function hasRole(me: Me, role: Role): boolean {
  return me.roles ? me.roles.includes(role) : me.role === role;
}

export type Session =
  | { status: "loading" }
  | { status: "anon" }
  | { status: "user"; me: Me }
  | { status: "error"; code: string };

type SessionApi = {
  session: Session;
  /** Reloads GET /auth/me; returns the user or null when signed out or on error. */
  refresh: () => Promise<Me | null>;
  /** Call after any sign-in: reloads the user and routes to `next` (or two-factor first). */
  afterSignIn: (next?: string) => Promise<void>;
  /** Ends the session; returns the error code when the server did not, else null. */
  signOut: () => Promise<string | null>;
};

const SessionContext = createContext<SessionApi | null>(null);

export const roleLabel: Record<Role, string> = {
  school_admin: "Autoridad escolar",
  officer: "Comisión del centro",
  student: "Estudiante",
};

/** Where a user belongs right now, given the two-factor state of the session. */
export function homeFor(me: Me): string {
  if (me.mfa === "enrollment_required") return "/seguridad/activar";
  if (me.mfa === "pending") return "/seguridad/verificar";
  return "/";
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session>({ status: "loading" });

  const refresh = useCallback(async () => {
    const result = await call(apiClient.GET("/auth/me"));
    if (result.ok) {
      setSession({ status: "user", me: result.data });
      return result.data;
    }
    setSession(result.code === "unauthenticated" ? { status: "anon" } : { status: "error", code: result.code });
    return null;
  }, []);

  const afterSignIn = useCallback(
    async (next?: string) => {
      const target = safeNext(next ?? readNext(window.location.search));
      const me = await refresh();
      if (!me) {
        // Without a user, target shows the retry screen (or the sign-in page): never a pending form.
        navigate(target, { replace: true });
        return;
      }
      const home = homeFor(me);
      navigate(destinationFor(home, target), { replace: true });
    },
    [refresh],
  );

  const signOut = useCallback(async () => {
    const result = await call(apiClient.POST("/auth/logout"));
    // 401: the session was already gone. Any other failure: the cookie may still be valid.
    if (!result.ok && result.code !== "unauthenticated") return result.code;
    setSession({ status: "anon" });
    navigate("/ingresar", { replace: true });
    return null;
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const value = useMemo(() => ({ session, refresh, afterSignIn, signOut }), [session, refresh, afterSignIn, signOut]);
  return <SessionContext value={value}>{children}</SessionContext>;
}

export function useSession(): SessionApi {
  const api = useContext(SessionContext);
  if (!api) throw new Error("useSession outside SessionProvider");
  return api;
}

/** The signed-in user; only for screens rendered behind the route guard. */
export function useMe(): Me {
  const { session } = useSession();
  if (session.status !== "user") throw new Error("useMe without a signed-in user");
  return session.me;
}
