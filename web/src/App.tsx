import type { JSX } from "react";
import { errorMessage } from "./api/errors.ts";
import { hasRole, homeFor, SessionProvider, useSession } from "./auth/session.tsx";
import { AppShell } from "./components/AppShell.tsx";
import { AuthLayout } from "./components/AuthLayout.tsx";
import { PageTitle, useDocumentTitle } from "./components/Page.tsx";
import { captureFragmentToken } from "./lib/fragment.ts";
import { destinationFor, readNext, safeNext, withNext } from "./lib/next.ts";
import { Redirect, usePathname } from "./lib/router.tsx";
import { Claim } from "./screens/Claim.tsx";
import { CommissionPanel } from "./screens/CommissionPanel.tsx";
import { Election } from "./screens/Election.tsx";
import { Calculator } from "./screens/Calculator.tsx";
import { ContactHeadings } from "./screens/ContactHeadings.tsx";
import { Contacts } from "./screens/Contacts.tsx";
import { Home } from "./screens/Home.tsx";
import { Finance } from "./screens/Finance.tsx";
import { MfaEnroll } from "./screens/MfaEnroll.tsx";
import { MfaVerify } from "./screens/MfaVerify.tsx";
import { Members } from "./screens/Members.tsx";
import { ResetPassword } from "./screens/ResetPassword.tsx";
import { SignIn } from "./screens/SignIn.tsx";
import { Roster } from "./screens/Roster.tsx";
import { NotFound, Security } from "./screens/Simple.tsx";
import { VerifyEmail } from "./screens/VerifyEmail.tsx";

export function App() {
  // Before any request: move an emailed `#token=` out of the address bar.
  captureFragmentToken();
  return (
    <SessionProvider>
      <Routes />
    </SessionProvider>
  );
}

const publicRoutes: Record<string, () => JSX.Element> = {
  "/ingresar": SignIn,
  "/alta": Claim,
  "/verificar": VerifyEmail,
  "/restablecer": ResetPassword,
};

const shellRoutes: Record<string, { page: string; Screen: () => JSX.Element }> = {
  "/": { page: "Inicio", Screen: Home },
  "/caja": { page: "Caja", Screen: Finance },
  "/padron": { page: "Padrón", Screen: Roster },
  "/miembros": { page: "Miembros", Screen: Members },
  "/eleccion": { page: "Elección", Screen: Election },
  "/centro-de-estudiantes": { page: "Centro de estudiantes", Screen: CommissionPanel },
  "/calculadora": { page: "Calculadora", Screen: Calculator },
  "/contactos": { page: "Contactos", Screen: Contacts },
  "/contactos/rubros": { page: "Rubros", Screen: ContactHeadings },
  "/seguridad": { page: "Seguridad", Screen: Security },
};

function Routes() {
  const path = usePathname();
  const { session } = useSession();

  if (session.status === "loading") {
    return (
      <p className="loading" aria-busy="true">
        Cargando…
      </p>
    );
  }

  const Public = publicRoutes[path];
  if (Public) return <Public />;
  const isMfaRoute = path === "/seguridad/activar" || path === "/seguridad/verificar";
  const nextFromHere = isMfaRoute ? readNext(window.location.search) : safeNext(path + window.location.search);
  if (session.status === "error") return <SessionError code={session.code} />;
  if (session.status === "anon") return <Redirect to={withNext("/ingresar", nextFromHere)} />;

  // Two-factor comes first: until it is done, the only place to go is its screen.
  const me = session.me;
  const home = homeFor(me);
  if (isMfaRoute || home !== "/") {
    if (path !== home) return <Redirect to={destinationFor(home, nextFromHere)} />;
    return home === "/seguridad/activar" ? <MfaEnroll /> : <MfaVerify />;
  }
  if ((path === "/padron" || path === "/miembros") && !hasRole(me, "school_admin")) return <Redirect to="/" />;
  if ((path === "/eleccion" || path === "/centro-de-estudiantes") && me.role === null) return <Redirect to="/" />;

  const route = shellRoutes[path];
  return (
    <AppShell page={route?.page ?? "No encontrada"}>{route ? <route.Screen /> : <NotFound />}</AppShell>
  );
}

function SessionError({ code }: { code: string }) {
  useDocumentTitle("Sin conexión");
  const { refresh } = useSession();
  return (
    <AuthLayout>
      <PageTitle>No pudimos cargar la página</PageTitle>
      <p className="lead">{errorMessage(code)}</p>
      <button type="button" className="button button--primary button--block" onClick={() => void refresh()}>
        Reintentar
      </button>
    </AuthLayout>
  );
}
