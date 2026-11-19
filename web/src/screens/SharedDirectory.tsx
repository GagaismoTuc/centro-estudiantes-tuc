import { useCallback, useEffect, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { canReadContacts, hasRole, useMe, useSession } from "../auth/session.tsx";
import { FormStatus, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { formatPesos } from "../lib/money.ts";
import { Link } from "../lib/router.tsx";
import { ContactMeta, NoContactsAccess } from "./contactShared.tsx";

type SharedContact = components["schemas"]["SharedContact"];

/** Shared contact directory across schools of the installation (never shows school or notes); Officers can copy entries into their own directory. */
export function SharedDirectory() {
  useDocumentTitle("Directorio compartido");
  const me = useMe();
  const { refresh } = useSession();

  const canRead = canReadContacts(me);
  const canCopy = hasRole(me, "officer");
  const [items, setItems] = useState<SharedContact[]>([]);
  const [headings, setHeadings] = useState<string[]>([]);
  const [headingFilter, setHeadingFilter] = useState("");
  const [zoneFilter, setZoneFilter] = useState("");
  const [page, setPage] = useState(1);
  const [totalPages, setTotalPages] = useState(0);
  const [loading, setLoading] = useState(canRead);
  const [status, setStatus] = useState<Status>(null);
  const [copyingId, setCopyingId] = useState<string | null>(null);

  const statusRef = useRef<HTMLDivElement>(null);
  const seqRef = useRef(0);

  const handleAuthError = useCallback(
    (code: string) => {
      if (code === "mfa_required" || code === "unauthenticated") void refresh();
    },
    [refresh],
  );

  useEffect(() => {
    if (!canRead) return;
    const seq = ++seqRef.current;
    setLoading(true);
    setStatus(null);
    const trimmedZone = zoneFilter.trim();
    const query: { heading?: string; zone?: string; page?: number } = { page };
    if (headingFilter) query.heading = headingFilter;
    if (trimmedZone) query.zone = trimmedZone;
    void call(
      apiClient.GET("/shared-contacts", {
        params: { query },
      }),
    ).then((res) => {
      if (seq !== seqRef.current) return;
      setLoading(false);
      if (res.ok) {
        setItems(res.data.items);
        setHeadings(res.data.headings);
        setTotalPages(res.data.totalPages);
        return;
      }
      handleAuthError(res.code);
      setStatus({ tone: "error", text: errorMessage(res.code) });
      statusRef.current?.focus();
    });
  }, [canRead, headingFilter, zoneFilter, page, handleAuthError]);

  async function copyContact(contact: SharedContact) {
    if (copyingId !== null) return;
    setCopyingId(contact.id);
    setStatus(null);
    const res = await call(
      apiClient.POST("/shared-contacts/{id}/copy", {
        params: { path: { id: contact.id } },
      }),
    );
    setCopyingId(null);
    if (res.ok) {
      setStatus({
        tone: "ok",
        text: `Se copió a ${res.data.name} a tus contactos (rubro ${res.data.headingName}).`,
      });
      return;
    }
    handleAuthError(res.code);
    setStatus({ tone: "error", text: errorMessage(res.code) });
    statusRef.current?.focus();
  }

  if (!canRead) {
    return (
      <NoContactsAccess
        title="Directorio compartido"
        description="Solo la comisión directiva, la escuela y el veedor pueden ver el directorio compartido."
        headingId="no-shared-access-title"
      />
    );
  }

  const hasFilters = headingFilter !== "" || zoneFilter.trim() !== "";

  return (
    <>
      <header className="page-header">
        <div>
          <PageTitle eyebrow="Comisión directiva">Directorio compartido</PageTitle>
          <p className="page-header__school">
            Contactos aprobados compartidos por escuelas de la red. Nunca muestran notas internas ni la escuela de origen.
          </p>
        </div>
        <div className="page-actions">
          <Link className="button button--secondary" to="/contactos">
            Volver a contactos
          </Link>
        </div>
      </header>

      <FormStatus status={status} ref={statusRef} />

      <section className="panel" aria-label="Filtros">
        <div className="contacts-filters">
          <div className="field">
            <label htmlFor="shared-heading-filter">Rubro</label>
            <select
              id="shared-heading-filter"
              value={headingFilter}
              onChange={(e) => {
                setHeadingFilter(e.target.value);
                setPage(1);
              }}
            >
              <option value="">Todos los rubros</option>
              {headings.map((h) => (
                <option key={h} value={h}>
                  {h}
                </option>
              ))}
            </select>
          </div>

          <div className="field">
            <label htmlFor="shared-zone-filter">Zona o ciudad</label>
            <input
              id="shared-zone-filter"
              type="search"
              placeholder="Ej. Yerba Buena"
              value={zoneFilter}
              onChange={(e) => {
                setZoneFilter(e.target.value);
                setPage(1);
              }}
            />
          </div>
        </div>
      </section>

      {loading ? (
        <p className="loading" aria-busy="true">
          Cargando directorio compartido…
        </p>
      ) : items.length === 0 ? (
        <section className="panel">
          <p className="field__hint">
            {hasFilters
              ? "Ningún contacto compartido coincide con los filtros."
              : "Todavía no hay contactos aprobados en el directorio compartido."}
          </p>
        </section>
      ) : (
        <>
          <div className="contacts-grid">
            {items.map((contact) => (
              <article key={contact.id} className="panel contact-card" data-testid={`shared-contact-${contact.id}`}>
                <div className="contact-card__header">
                  <div className="contact-card__title">
                    <h2>
                      <bdi>{contact.name}</bdi>
                    </h2>
                    <span className="badge">{contact.headingName}</span>
                  </div>
                  {canCopy && (
                    <div className="panel__actions">
                      <button
                        type="button"
                        className="button button--secondary button--small"
                        disabled={copyingId !== null}
                        aria-busy={copyingId === contact.id || undefined}
                        onClick={() => void copyContact(contact)}
                      >
                        {copyingId === contact.id ? "Copiando…" : "Copiar a nuestros contactos"}
                      </button>
                    </div>
                  )}
                </div>

                <ContactMeta phone={contact.phone} email={contact.email} zone={contact.zone} />

                <div className="contact-prices">
                  <div className="contact-prices__header">
                    <h3>Precios de referencia</h3>
                  </div>
                  {contact.referencePrices.length === 0 ? (
                    <p className="field__hint">Sin precios de referencia cargados.</p>
                  ) : (
                    <ul className="contact-prices__list">
                      {contact.referencePrices.map((price, idx) => (
                        <li key={`${contact.id}-${idx}`} className="contact-prices__item">
                          <div className="contact-prices__info">
                            <span>
                              <bdi>{price.description}</bdi>
                            </span>
                            <span className="contact-prices__amount">{formatPesos(price.amountCents)}</span>
                            <span className="contact-prices__date">{price.date}</span>
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              </article>
            ))}
          </div>

          {totalPages > 1 && (
            <nav className="shared-pagination" aria-label="Paginación">
              <button
                type="button"
                className="button button--secondary button--small"
                disabled={page <= 1}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
              >
                Anterior
              </button>
              <span className="field__hint">
                Página {page} de {totalPages}
              </span>
              <button
                type="button"
                className="button button--secondary button--small"
                disabled={page >= totalPages}
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              >
                Siguiente
              </button>
            </nav>
          )}
        </>
      )}
    </>
  );
}
