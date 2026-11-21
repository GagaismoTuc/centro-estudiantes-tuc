import { useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { hasRole, roleLabels, useMe } from "../auth/session.tsx";
import { FormStatus, type Status } from "../components/Form.tsx";
import { Icon, type IconName } from "../components/Icon.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";

type Module = { title: string; text: string; icon: IconName };

// Finance and elections are for anyone in a school (/eleccion sends users without one home).
const schoolModules: (Module & { to: string })[] = [
  { title: "Finanzas", text: "Ingresos y gastos del centro, con comprobantes.", icon: "wallet", to: "/caja" },
  { title: "Elecciones", text: "Votación desde el celular, con voto secreto.", icon: "ballot", to: "/eleccion" },
];

const upcoming: Module[] = [{ title: "Notas", text: "Notas formales con el membrete de tu escuela.", icon: "note" }];

function VerifyEmailBanner({ email }: { email: string }) {
  const [status, setStatus] = useState<Status>(null);
  const [pending, setPending] = useState(false);

  async function send() {
    setPending(true);
    const result = await call(apiClient.POST("/auth/email/verification"));
    setPending(false);
    setStatus(
      result.ok
        ? { tone: "ok", text: `Te enviamos un enlace nuevo a ${email}. Vence en 48 horas.` }
        : { tone: "error", text: errorMessage(result.code) },
    );
  }

  return (
    <section className="banner" aria-labelledby="verify-title">
      <Icon name="mail" />
      <div className="banner__body">
        <h2 id="verify-title">Verificá tu mail</h2>
        <p>
          Cuando creaste la cuenta te enviamos un enlace a <bdi>{email}</bdi>. Abrilo para poder ingresar con un enlace y
          recuperar tu contraseña si la olvidás.
        </p>
        <p className="muted small">¿No lo encontrás? Revisá el correo no deseado. Si pedís otro, el enlace anterior deja de servir.</p>
        <FormStatus status={status} />
      </div>
      <button type="button" className="button button--secondary" disabled={pending} onClick={() => void send()}>
        Reenviar
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
          <PageTitle eyebrow="Inicio">¡Hola!</PageTitle>
          <p className="page-header__school">
            {me.tenantName !== null && me.role ? (
              <>
                <bdi>{me.tenantName}</bdi> · {roleLabels(me)}
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
          {hasRole(me, "school_admin") && (
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
          {me.role !== null &&
            schoolModules.map((module) => (
              <li key={module.title}>
                <Link to={module.to} className="card card--link">
                  <span className="card__icon">
                    <Icon name={module.icon} />
                  </span>
                  <span className="card__title">{module.title}</span>
                  <span className="card__text">{module.text}</span>
                  <span className="card__cta">
                    Abrir <Icon name="arrow" />
                  </span>
                </Link>
              </li>
            ))}
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
