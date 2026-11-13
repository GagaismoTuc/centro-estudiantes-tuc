import { useMe } from "../auth/session.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";

/** /seguridad: two-factor status of the account. */
export function Security() {
  useDocumentTitle("Seguridad");
  const me = useMe();
  return (
    <>
      <header className="page-header">
        <PageTitle eyebrow="Cuenta">Seguridad</PageTitle>
      </header>
      <section className="panel" aria-labelledby="mfa-title">
        <h2 id="mfa-title">Verificación en dos pasos</h2>
        {me.mfa === "satisfied" && <p>Está activada. Cada vez que ingresás te pedimos el código de tu app.</p>}
        {me.mfa === "not_required" && <p>Tu rol no la necesita. Tu cuenta se protege con tu contraseña y tu mail.</p>}
        <p className="muted">
          Si perdés el celular y los códigos de recuperación, pedile a quien administra la plataforma que la restablezca.
        </p>
      </section>
    </>
  );
}

/** /padron: roster screens arrive in T6b. */
export function Roster() {
  useDocumentTitle("Padrón");
  return (
    <>
      <header className="page-header">
        <PageTitle eyebrow="Escuela">Padrón</PageTitle>
      </header>
      <section className="panel">
        <p>Acá vas a poder cargar el padrón desde un CSV y entregar los códigos de acceso. Lo estamos terminando.</p>
      </section>
    </>
  );
}

export function NotFound() {
  useDocumentTitle("Página no encontrada");
  return (
    <>
      <header className="page-header">
        <PageTitle>No encontramos esta página</PageTitle>
      </header>
      <p>
        <Link to="/">Volver al inicio</Link>
      </p>
    </>
  );
}
