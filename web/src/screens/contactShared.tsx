import { Icon } from "../components/Icon.tsx";
import { PageTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";

export function NoContactsAccess({ title, description, headingId }: { title: string; description: string; headingId: string }) {
  return (
    <>
      <header className="page-header">
        <PageTitle eyebrow="Comisión directiva">{title}</PageTitle>
      </header>
      <section className="banner" role="alert" aria-labelledby={headingId}>
        <Icon name="alert" />
        <div className="banner__body">
          <h2 id={headingId}>No tenés acceso a esta sección</h2>
          <p>{description}</p>
        </div>
        <Link className="button button--secondary" to="/">
          Volver al inicio
        </Link>
      </section>
    </>
  );
}

export function ContactMeta({ phone, email, zone }: { phone: string; email: string; zone: string }) {
  return (
    <dl className="contact-card__meta">
      {phone && (
        <div>
          <dt>Teléfono / WhatsApp</dt>
          <dd>
            <bdi>{phone}</bdi>
          </dd>
        </div>
      )}
      {email && (
        <div>
          <dt>Mail</dt>
          <dd>
            <bdi>{email}</bdi>
          </dd>
        </div>
      )}
      {zone && (
        <div>
          <dt>Zona o ciudad</dt>
          <dd>
            <bdi>{zone}</bdi>
          </dd>
        </div>
      )}
    </dl>
  );
}
