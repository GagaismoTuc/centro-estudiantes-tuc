import { useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { roleLabel, useMe } from "../auth/session.tsx";
import { FormStatus, type Status } from "../components/Form.tsx";
import { Icon, type IconName } from "../components/Icon.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";

const upcoming: { title: string; text: string; icon: IconName }[] = [
  { title: "Finanzas", text: "Ingresos y gastos del centro, con comprobantes.", icon: "wallet" },
  { title: "Elecciones", text: "Votación desde el celular, con voto secreto.", icon: "ballot" },
  { title: "Notas", text: "Notas formales con el membrete de tu escuela.", icon: "note" },
];

function VerifyEmailBanner({ email }: { email: string }) {
  const [status, setStatus] = useState<Status>(null);
  const [pending, setPending] = useState(false);

  async function send() {
    setPending(true);
    const result = await call(apiClient.POST("/auth/email/verification"));
    setPending(false);
    setStatus(
      result.ok
        ? { tone: "ok", text: `Te enviamos un enlace a ${email}. Vence en 48 horas.` }
        : { tone: "error", text: errorMessage(result.code) },
    );
  }

  return (
    <section className="banner" aria-labelledby="verify-title">
      <Icon name="mail" />
      <div className="banner__body">
        <h2 id="verify-title">Verificá tu mail</h2>
        <p>Así podés ingresar con un enlace y recuperar tu contraseña si la olvidás.</p>
        <FormStatus status={status} />
      </div>
      <button type="button" className="button button--secondary" disabled={pending} onClick={() => void send()}>
        Enviar enlace de verificación
      </button>
    </section>
  );
}

export function Home() {
  useDocumentTitle("Inicio");
  const me = useMe();
  return (
    <>
      <header className="page-header">
        <div>
          <PageTitle eyebrow="Inicio">Hola, {me.email}</PageTitle>
          <p className="page-header__school">
            {me.tenantName !== null && me.role ? (
              <>
                <bdi>{me.tenantName}</bdi> · {roleLabel[me.role]}
              </>
            ) : (
              "Sin escuela"
            )}
          </p>
        </div>
      </header>

      {!me.emailVerified && <VerifyEmailBanner email={me.email} />}

      {me.role === null && (
        <section className="banner" aria-labelledby="no-school-title">
          <Icon name="alert" />
          <div className="banner__body">
            <h2 id="no-school-title">Todavía no estás en ninguna escuela</h2>
            <p>Si tu escuela te dio un código de acceso, canjealo para sumarte.</p>
          </div>
          <Link className="button button--primary" to="/alta">
            Canjear código
          </Link>
        </section>
      )}

      <section aria-labelledby="modules-title">
        <h2 id="modules-title" className="section-title">
          Módulos
        </h2>
        <ul className="cards">
          {me.role === "school_admin" && (
            <li>
              <Link to="/padron" className="card card--link">
                <span className="card__icon">
                  <Icon name="users" />
                </span>
                <span className="card__title">Padrón</span>
                <span className="card__text">Cargá el padrón y entregá los códigos de acceso.</span>
                <span className="card__cta">
                  Abrir <Icon name="arrow" />
                </span>
              </Link>
            </li>
          )}
          {upcoming.map((module) => (
            <li key={module.title}>
              <div className="card card--muted">
                <span className="card__icon">
                  <Icon name={module.icon} />
                </span>
                <span className="card__title">{module.title}</span>
                <span className="card__text">{module.text}</span>
                <span className="badge">Próximamente</span>
              </div>
            </li>
          ))}
        </ul>
      </section>
    </>
  );
}
