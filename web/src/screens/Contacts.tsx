import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { canReadContacts, hasRole, useMe, useSession } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { FormStatus, SubmitButton, type Status } from "../components/Form.tsx";
import { Icon } from "../components/Icon.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";

type Heading = components["schemas"]["ContactHeading"];
type Contact = components["schemas"]["Contact"];

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

function formatPesos(cents: number): string {
  const whole = Math.floor(cents / 100);
  const rem = cents % 100;
  const formattedWhole = whole.toLocaleString("es-AR");
  return rem === 0 ? `$${formattedWhole}` : `$${formattedWhole},${String(rem).padStart(2, "0")}`;
}

function parsePesosToCents(raw: string): number | null {
  const cleaned = raw.trim().replace(/\s+/g, "").replace(",", ".");
  if (!/^\d+(\.\d{1,2})?$/.test(cleaned)) return null;
  const [wholePart, fracPart = ""] = cleaned.split(".");
  const cents = Number(wholePart) * 100 + Number(fracPart.padEnd(2, "0"));
  return Number.isSafeInteger(cents) && cents > 0 ? cents : null;
}

function todayLocalISO(): string {
  const d = new Date();
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

type ContactFormValues = {
  name: string;
  headingId: string;
  phone: string;
  email: string;
  zone: string;
  notes: string;
};

type ContactField = "name" | "headingId" | "email";

function omitField<K extends string>(prev: Partial<Record<K, string>>, key: K): Partial<Record<K, string>> {
  if (!(key in prev)) return prev;
  const next = { ...prev };
  delete next[key];
  return next;
}

function ContactForm({
  title,
  submitLabel,
  headings,
  initial,
  onSave,
  onCancel,
  onAuthError,
}: {
  title: string;
  submitLabel: string;
  headings: Heading[];
  initial?: Contact;
  onSave: (contact: Contact) => void;
  onCancel: () => void;
  onAuthError: (code: string) => void;
}) {
  const baseId = useId();
  const activeHeadings = useMemo(() => headings.filter((h) => !h.archived), [headings]);
  const selectableHeadings = useMemo(() => {
    if (!initial || !initial.headingArchived) return activeHeadings;
    const current = headings.find((h) => h.id === initial.headingId);
    return current ? [current, ...activeHeadings] : activeHeadings;
  }, [activeHeadings, headings, initial]);

  const [values, setValues] = useState<ContactFormValues>({
    name: initial?.name ?? "",
    headingId: initial?.headingId ?? activeHeadings[0]?.id ?? "",
    phone: initial?.phone ?? "",
    email: initial?.email ?? "",
    zone: initial?.zone ?? "",
    notes: initial?.notes ?? "",
  });
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<ContactField, string>>>({});
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);

  const nameRef = useRef<HTMLInputElement>(null);
  const headingRef = useRef<HTMLSelectElement>(null);
  const emailRef = useRef<HTMLInputElement>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const focusNext = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!focusNext.current) return;
    focusNext.current.focus();
    focusNext.current = null;
  });

  function focusFirstError(errs: Partial<Record<ContactField, string>>) {
    const order: [ContactField, HTMLElement | null][] = [
      ["name", nameRef.current],
      ["headingId", headingRef.current],
      ["email", emailRef.current],
    ];
    for (const [key, el] of order) {
      if (errs[key] && el) {
        focusNext.current = el;
        return;
      }
    }
    focusNext.current = statusRef.current;
  }

  async function handleSubmit() {
    const errs: Partial<Record<ContactField, string>> = {};
    if (!values.name.trim()) errs.name = "Escribí el nombre del contacto.";
    if (!values.headingId) errs.headingId = "Elegí un rubro activo.";
    if (values.email.trim() && !EMAIL_RE.test(values.email.trim())) {
      errs.email = "Revisá el mail: tiene que ser como nombre@dominio.com.";
    }
    if (Object.keys(errs).length > 0) {
      setFieldErrors(errs);
      setStatus({ tone: "error", text: "Revisá los campos marcados." });
      focusFirstError(errs);
      return;
    }

    setBusy(true);
    setFieldErrors({});
    setStatus(null);
    const body = {
      name: values.name.trim(),
      headingId: values.headingId,
      phone: values.phone.trim(),
      email: values.email.trim(),
      zone: values.zone.trim(),
      notes: values.notes.trim(),
    };
    const result = initial
      ? await call(apiClient.PATCH("/contacts/{id}", { params: { path: { id: initial.id } }, body }))
      : await call(apiClient.POST("/contacts", { body }));
    setBusy(false);

    if (result.ok) {
      onSave(result.data);
      return;
    }
    if (result.code === "mfa_required" || result.code === "unauthenticated") {
      onAuthError(result.code);
      return;
    }
    const serverErrs: Partial<Record<ContactField, string>> = {};
    const msg = errorMessage(result.code);
    if (result.code === "name_required") serverErrs.name = msg;
    else if (result.code === "invalid_heading") serverErrs.headingId = msg;
    else if (result.code === "invalid_email") serverErrs.email = msg;
    setFieldErrors(serverErrs);
    setStatus({ tone: "error", text: msg });
    focusFirstError(serverErrs);
  }

  return (
    <section className="panel">
      <h2>{title}</h2>
      <form
        noValidate
        aria-label={title}
        onSubmit={(e) => {
          e.preventDefault();
          void handleSubmit();
        }}
      >
        <FormStatus status={status} ref={statusRef} />
        <div className="contacts-form-grid">
          <div className="field">
            <label htmlFor={`${baseId}-name`}>Nombre</label>
            <input
              id={`${baseId}-name`}
              ref={nameRef}
              name="name"
              type="text"
              autoComplete="organization"
              value={values.name}
              aria-invalid={fieldErrors.name ? true : undefined}
              aria-describedby={fieldErrors.name ? `${baseId}-name-err` : undefined}
              onChange={(e) => {
                setValues((v) => ({ ...v, name: e.target.value }));
                setFieldErrors((f) => omitField(f, "name"));
              }}
            />
            {fieldErrors.name && (
              <p id={`${baseId}-name-err`} className="error field__error" role="alert">
                {fieldErrors.name}
              </p>
            )}
          </div>

          <div className="field">
            <label htmlFor={`${baseId}-heading`}>Rubro</label>
            <select
              id={`${baseId}-heading`}
              ref={headingRef}
              name="headingId"
              value={values.headingId}
              aria-invalid={fieldErrors.headingId ? true : undefined}
              aria-describedby={fieldErrors.headingId ? `${baseId}-heading-err` : undefined}
              onChange={(e) => {
                setValues((v) => ({ ...v, headingId: e.target.value }));
                setFieldErrors((f) => omitField(f, "headingId"));
              }}
            >
              <option value="">Elegí un rubro</option>
              {selectableHeadings.map((h) => (
                <option key={h.id} value={h.id}>
                  {h.archived ? `${h.name} (archivado)` : h.name}
                </option>
              ))}
            </select>
            {fieldErrors.headingId && (
              <p id={`${baseId}-heading-err`} className="error field__error" role="alert">
                {fieldErrors.headingId}
              </p>
            )}
          </div>

          <div className="field">
            <label htmlFor={`${baseId}-phone`}>Teléfono / WhatsApp</label>
            <input
              id={`${baseId}-phone`}
              name="phone"
              type="tel"
              placeholder="381 555-0000"
              value={values.phone}
              onChange={(e) => setValues((v) => ({ ...v, phone: e.target.value }))}
            />
          </div>

          <div className="field">
            <label htmlFor={`${baseId}-email`}>Mail</label>
            <input
              id={`${baseId}-email`}
              ref={emailRef}
              name="email"
              type="email"
              value={values.email}
              aria-invalid={fieldErrors.email ? true : undefined}
              aria-describedby={fieldErrors.email ? `${baseId}-email-err` : undefined}
              onChange={(e) => {
                setValues((v) => ({ ...v, email: e.target.value }));
                setFieldErrors((f) => omitField(f, "email"));
              }}
            />
            {fieldErrors.email && (
              <p id={`${baseId}-email-err`} className="error field__error" role="alert">
                {fieldErrors.email}
              </p>
            )}
          </div>

          <div className="field">
            <label htmlFor={`${baseId}-zone`}>Zona o ciudad</label>
            <input
              id={`${baseId}-zone`}
              name="zone"
              type="text"
              placeholder="Yerba Buena"
              value={values.zone}
              onChange={(e) => setValues((v) => ({ ...v, zone: e.target.value }))}
            />
          </div>
        </div>

        <div className="field">
          <label htmlFor={`${baseId}-notes`}>Notas</label>
          <textarea
            id={`${baseId}-notes`}
            name="notes"
            value={values.notes}
            onChange={(e) => setValues((v) => ({ ...v, notes: e.target.value }))}
          />
        </div>

        <div className="panel__actions">
          <SubmitButton pending={busy}>
            {busy ? "Guardando…" : submitLabel}
          </SubmitButton>
          <button type="button" className="button button--secondary" disabled={busy} onClick={onCancel}>
            Cancelar
          </button>
        </div>
      </form>
    </section>
  );
}

type PriceField = "description" | "amount" | "date";

function ReferencePriceForm({
  contactId,
  onAdded,
  onCancel,
  onAuthError,
}: {
  contactId: string;
  onAdded: (price: components["schemas"]["ReferencePrice"]) => void;
  onCancel: () => void;
  onAuthError: (code: string) => void;
}) {
  const baseId = useId();
  const [description, setDescription] = useState("");
  const [amount, setAmount] = useState("");
  const [date, setDate] = useState(todayLocalISO());
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<PriceField, string>>>({});
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);

  const descRef = useRef<HTMLInputElement>(null);
  const amountRef = useRef<HTMLInputElement>(null);
  const dateRef = useRef<HTMLInputElement>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const focusNext = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!focusNext.current) return;
    focusNext.current.focus();
    focusNext.current = null;
  });

  function focusFirst(errs: Partial<Record<PriceField, string>>) {
    const order: [PriceField, HTMLElement | null][] = [
      ["description", descRef.current],
      ["amount", amountRef.current],
      ["date", dateRef.current],
    ];
    for (const [key, el] of order) {
      if (errs[key] && el) {
        focusNext.current = el;
        return;
      }
    }
    focusNext.current = statusRef.current;
  }

  async function handleSubmit() {
    const errs: Partial<Record<PriceField, string>> = {};
    if (!description.trim()) errs.description = "Escribí una descripción.";
    const amountCents = parsePesosToCents(amount);
    if (amountCents === null) errs.amount = "Ingresá un monto mayor a cero.";
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) errs.date = "Ingresá una fecha válida.";
    else if (date > todayLocalISO()) errs.date = "La fecha no puede ser futura.";

    if (Object.keys(errs).length > 0 || amountCents === null) {
      setFieldErrors(errs);
      setStatus({ tone: "error", text: "Revisá los datos del precio." });
      focusFirst(errs);
      return;
    }

    setBusy(true);
    setFieldErrors({});
    setStatus(null);
    const result = await call(
      apiClient.POST("/contacts/{id}/reference-prices", {
        params: { path: { id: contactId } },
        body: { description: description.trim(), amountCents, date },
      }),
    );
    setBusy(false);

    if (result.ok) {
      onAdded(result.data);
      return;
    }
    if (result.code === "mfa_required" || result.code === "unauthenticated") {
      onAuthError(result.code);
      return;
    }
    const serverErrs: Partial<Record<PriceField, string>> = {};
    const msg = errorMessage(result.code);
    if (result.code === "description_required") serverErrs.description = msg;
    else if (result.code === "invalid_amount") serverErrs.amount = msg;
    else if (result.code === "future_date" || result.code === "invalid_date") serverErrs.date = msg;
    setFieldErrors(serverErrs);
    setStatus({ tone: "error", text: msg });
    focusFirst(serverErrs);
  }

  return (
    <form
      noValidate
      aria-label="Nuevo precio de referencia"
      onSubmit={(e) => {
        e.preventDefault();
        void handleSubmit();
      }}
    >
      <FormStatus status={status} ref={statusRef} />
      <div className="contacts-form-grid">
        <div className="field">
          <label htmlFor={`${baseId}-desc`}>Descripción</label>
          <input
            id={`${baseId}-desc`}
            ref={descRef}
            name="description"
            type="text"
            placeholder="Gazebo 3×3"
            value={description}
            aria-invalid={fieldErrors.description ? true : undefined}
            aria-describedby={fieldErrors.description ? `${baseId}-desc-err` : undefined}
            onChange={(e) => {
              setDescription(e.target.value);
              setFieldErrors((f) => omitField(f, "description"));
            }}
          />
          {fieldErrors.description && (
            <p id={`${baseId}-desc-err`} className="error field__error" role="alert">
              {fieldErrors.description}
            </p>
          )}
        </div>

        <div className="field">
          <label htmlFor={`${baseId}-amount`}>Monto ($)</label>
          <input
            id={`${baseId}-amount`}
            ref={amountRef}
            name="amount"
            type="text"
            inputMode="decimal"
            placeholder="25000"
            value={amount}
            aria-invalid={fieldErrors.amount ? true : undefined}
            aria-describedby={fieldErrors.amount ? `${baseId}-amount-err` : undefined}
            onChange={(e) => {
              setAmount(e.target.value);
              setFieldErrors((f) => omitField(f, "amount"));
            }}
          />
          {fieldErrors.amount && (
            <p id={`${baseId}-amount-err`} className="error field__error" role="alert">
              {fieldErrors.amount}
            </p>
          )}
        </div>

        <div className="field">
          <label htmlFor={`${baseId}-date`}>Fecha</label>
          <input
            id={`${baseId}-date`}
            ref={dateRef}
            name="date"
            type="date"
            value={date}
            aria-invalid={fieldErrors.date ? true : undefined}
            aria-describedby={fieldErrors.date ? `${baseId}-date-err` : undefined}
            onChange={(e) => {
              setDate(e.target.value);
              setFieldErrors((f) => omitField(f, "date"));
            }}
          />
          {fieldErrors.date && (
            <p id={`${baseId}-date-err`} className="error field__error" role="alert">
              {fieldErrors.date}
            </p>
          )}
        </div>
      </div>

      <div className="panel__actions">
        <SubmitButton pending={busy}>
          {busy ? "Guardando…" : "Guardar precio"}
        </SubmitButton>
        <button type="button" className="button button--secondary" disabled={busy} onClick={onCancel}>
          Cancelar
        </button>
      </div>
    </form>
  );
}

/** Contact directory screen: readable by Officers, School admins, and Veedors; editable by Officers. */
export function Contacts() {
  useDocumentTitle("Contactos");
  const me = useMe();
  const { refresh } = useSession();

  const canRead = canReadContacts(me);
  const canWrite = hasRole(me, "officer");

  const [headings, setHeadings] = useState<Heading[]>([]);
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [headingFilter, setHeadingFilter] = useState("");
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(canRead);
  const [status, setStatus] = useState<Status>(null);
  const [creating, setCreating] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [addingPriceFor, setAddingPriceFor] = useState<string | null>(null);
  const [confirmDeleteContact, setConfirmDeleteContact] = useState<Contact | null>(null);
  const [confirmDeletePrice, setConfirmDeletePrice] = useState<{ contactId: string; priceId: string; description: string } | null>(null);

  const statusRef = useRef<HTMLDivElement>(null);

  const handleAuthError = useCallback(
    (code: string) => {
      if (code === "mfa_required" || code === "unauthenticated") void refresh();
    },
    [refresh],
  );

  const load = useCallback(async () => {
    if (!canRead) return;
    setLoading(true);
    const [hRes, cRes] = await Promise.all([
      call(apiClient.GET("/contacts/headings")),
      call(apiClient.GET("/contacts")),
    ]);
    setLoading(false);
    if (hRes.ok) setHeadings(hRes.data);
    else {
      handleAuthError(hRes.code);
      setStatus({ tone: "error", text: errorMessage(hRes.code) });
    }
    if (cRes.ok) setContacts(cRes.data);
    else {
      handleAuthError(cRes.code);
      setStatus({ tone: "error", text: errorMessage(cRes.code) });
    }
  }, [canRead, handleAuthError]);

  useEffect(() => {
    void load();
  }, [load]);

  const filteredContacts = useMemo(() => {
    const q = search.trim().toLowerCase();
    return contacts.filter((c) => {
      if (headingFilter && c.headingId !== headingFilter) return false;
      if (q && !c.name.toLowerCase().includes(q) && !c.zone.toLowerCase().includes(q)) return false;
      return true;
    });
  }, [contacts, headingFilter, search]);

  async function deleteContact(contact: Contact) {
    setConfirmDeleteContact(null);
    setStatus(null);
    const res = await call(apiClient.DELETE("/contacts/{id}", { params: { path: { id: contact.id } } }));
    if (res.ok) {
      setContacts((prev) => prev.filter((c) => c.id !== contact.id));
      setStatus({ tone: "ok", text: `Se borró a ${contact.name}.` });
      return;
    }
    handleAuthError(res.code);
    setStatus({ tone: "error", text: errorMessage(res.code) });
    statusRef.current?.focus();
  }

  async function deletePrice(target: { contactId: string; priceId: string; description: string }) {
    setConfirmDeletePrice(null);
    setStatus(null);
    const res = await call(
      apiClient.DELETE("/contacts/{id}/reference-prices/{priceId}", {
        params: { path: { id: target.contactId, priceId: target.priceId } },
      }),
    );
    if (res.ok) {
      setContacts((prev) =>
        prev.map((c) =>
          c.id === target.contactId
            ? { ...c, referencePrices: c.referencePrices.filter((p) => p.id !== target.priceId) }
            : c,
        ),
      );
      setStatus({ tone: "ok", text: "Se borró el precio de referencia." });
      return;
    }
    handleAuthError(res.code);
    setStatus({ tone: "error", text: errorMessage(res.code) });
    statusRef.current?.focus();
  }

  if (!canRead) {
    return (
      <>
        <header className="page-header">
          <PageTitle eyebrow="Comisión directiva">Contactos</PageTitle>
        </header>
        <section className="banner" role="alert" aria-labelledby="no-access-title">
          <Icon name="alert" />
          <div className="banner__body">
            <h2 id="no-access-title">No tenés acceso a esta sección</h2>
            <p>Solo la comisión directiva, la escuela y el veedor pueden ver el directorio de contactos.</p>
          </div>
          <Link className="button button--secondary" to="/">
            Volver al inicio
          </Link>
        </section>
      </>
    );
  }

  return (
    <>
      <header className="page-header">
        <div>
          <PageTitle eyebrow="Comisión directiva">Contactos</PageTitle>
          <p className="page-header__school">
            Directorio de contactos del centro. Se mantiene entre gestiones.
          </p>
        </div>
        {canWrite && (
          <div className="page-actions">
            <Link className="button button--secondary" to="/contactos/rubros">
              Manejar rubros
            </Link>
            <button
              type="button"
              className="button button--primary"
              onClick={() => {
                setEditingId(null);
                setCreating((v) => !v);
              }}
            >
              Nuevo contacto
            </button>
          </div>
        )}
      </header>

      <FormStatus status={status} ref={statusRef} />

      {canWrite && creating && (
        <ContactForm
          title="Nuevo contacto"
          submitLabel="Guardar contacto"
          headings={headings}
          onSave={(created) => {
            setContacts((prev) => [...prev, created].sort((a, b) => a.name.localeCompare(b.name, "es")));
            setCreating(false);
            setStatus({ tone: "ok", text: `Se guardó ${created.name}.` });
          }}
          onCancel={() => setCreating(false)}
          onAuthError={handleAuthError}
        />
      )}

      <section className="panel" aria-label="Filtros">
        <div className="contacts-filters">
          <div className="field">
            <label htmlFor="contacts-heading-filter">Rubro</label>
            <select
              id="contacts-heading-filter"
              value={headingFilter}
              onChange={(e) => setHeadingFilter(e.target.value)}
            >
              <option value="">Todos los rubros</option>
              {headings.map((h) => (
                <option key={h.id} value={h.id}>
                  {h.archived ? `${h.name} (archivado)` : h.name}
                </option>
              ))}
            </select>
          </div>

          <div className="field">
            <label htmlFor="contacts-search">Buscar por nombre o zona</label>
            <input
              id="contacts-search"
              type="search"
              placeholder="Ej. Gazebos o Yerba Buena"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
        </div>
      </section>

      {loading ? (
        <p className="loading" aria-busy="true">
          Cargando contactos…
        </p>
      ) : filteredContacts.length === 0 ? (
        <section className="panel">
          <p className="field__hint">
            {contacts.length === 0
              ? "Todavía no hay contactos cargados en el directorio."
              : "Ningún contacto coincide con los filtros."}
          </p>
        </section>
      ) : (
        <div className="contacts-grid">
          {filteredContacts.map((contact) =>
            editingId === contact.id && canWrite ? (
              <ContactForm
                key={contact.id}
                title={`Editar ${contact.name}`}
                submitLabel="Guardar cambios"
                headings={headings}
                initial={contact}
                onSave={(updated) => {
                  setContacts((prev) =>
                    prev
                      .map((c) => (c.id === updated.id ? updated : c))
                      .sort((a, b) => a.name.localeCompare(b.name, "es")),
                  );
                  setEditingId(null);
                  setStatus({ tone: "ok", text: `Se actualizó ${updated.name}.` });
                }}
                onCancel={() => setEditingId(null)}
                onAuthError={handleAuthError}
              />
            ) : (
              <article key={contact.id} className="panel contact-card" data-testid={`contact-${contact.id}`}>
                <div className="contact-card__header">
                  <div className="contact-card__title">
                    <h2>
                      <bdi>{contact.name}</bdi>
                    </h2>
                    <span className="badge">
                      {contact.headingArchived ? `${contact.headingName} (archivado)` : contact.headingName}
                    </span>
                  </div>
                  {canWrite && (
                    <div className="panel__actions">
                      <button
                        type="button"
                        className="button button--secondary button--small"
                        onClick={() => {
                          setCreating(false);
                          setEditingId(contact.id);
                        }}
                      >
                        Editar
                      </button>
                      <button
                        type="button"
                        className="button button--secondary button--small"
                        onClick={() => setConfirmDeleteContact(contact)}
                      >
                        Borrar contacto
                      </button>
                    </div>
                  )}
                </div>

                <dl className="contact-card__meta">
                  {contact.phone && (
                    <div>
                      <dt>Teléfono / WhatsApp</dt>
                      <dd>
                        <bdi>{contact.phone}</bdi>
                      </dd>
                    </div>
                  )}
                  {contact.email && (
                    <div>
                      <dt>Mail</dt>
                      <dd>
                        <bdi>{contact.email}</bdi>
                      </dd>
                    </div>
                  )}
                  {contact.zone && (
                    <div>
                      <dt>Zona o ciudad</dt>
                      <dd>
                        <bdi>{contact.zone}</bdi>
                      </dd>
                    </div>
                  )}
                </dl>

                {contact.notes && (
                  <p className="contact-card__notes">
                    <bdi>{contact.notes}</bdi>
                  </p>
                )}

                <div className="contact-prices">
                  <div className="contact-prices__header">
                    <h3>Precios de referencia</h3>
                    {canWrite && addingPriceFor !== contact.id && (
                      <button
                        type="button"
                        className="button button--secondary button--small"
                        onClick={() => setAddingPriceFor(contact.id)}
                      >
                        Agregar precio de referencia
                      </button>
                    )}
                  </div>

                  {canWrite && addingPriceFor === contact.id && (
                    <ReferencePriceForm
                      contactId={contact.id}
                      onAdded={(price) => {
                        setContacts((prev) =>
                          prev.map((c) =>
                            c.id === contact.id ? { ...c, referencePrices: [price, ...c.referencePrices] } : c,
                          ),
                        );
                        setAddingPriceFor(null);
                        setStatus({ tone: "ok", text: "Se agregó el precio de referencia." });
                      }}
                      onCancel={() => setAddingPriceFor(null)}
                      onAuthError={handleAuthError}
                    />
                  )}

                  {contact.referencePrices.length === 0 ? (
                    <p className="field__hint">Sin precios de referencia cargados.</p>
                  ) : (
                    <ul className="contact-prices__list">
                      {contact.referencePrices.map((price) => (
                        <li key={price.id} className="contact-prices__item">
                          <div className="contact-prices__info">
                            <span>
                              <bdi>{price.description}</bdi>
                            </span>
                            <span className="contact-prices__amount">{formatPesos(price.amountCents)}</span>
                            <span className="contact-prices__date">{price.date}</span>
                          </div>
                          {canWrite && (
                            <button
                              type="button"
                              className="button button--secondary button--small"
                              aria-label={`Borrar precio ${price.description}`}
                              onClick={() =>
                                setConfirmDeletePrice({
                                  contactId: contact.id,
                                  priceId: price.id,
                                  description: price.description,
                                })
                              }
                            >
                              Borrar
                            </button>
                          )}
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              </article>
            ),
          )}
        </div>
      )}

      {confirmDeleteContact && (
        <ConfirmDialog
          title="Borrar contacto"
          confirmLabel="Borrar"
          onConfirm={() => void deleteContact(confirmDeleteContact)}
          onCancel={() => setConfirmDeleteContact(null)}
        >
          <p>
            ¿Querés borrar a <strong>{confirmDeleteContact.name}</strong> y sus precios de referencia del directorio?
          </p>
        </ConfirmDialog>
      )}

      {confirmDeletePrice && (
        <ConfirmDialog
          title="Borrar precio de referencia"
          confirmLabel="Borrar"
          onConfirm={() => void deletePrice(confirmDeletePrice)}
          onCancel={() => setConfirmDeletePrice(null)}
        >
          <p>
            ¿Querés borrar el precio de referencia <strong>{confirmDeletePrice.description}</strong>?
          </p>
        </ConfirmDialog>
      )}
    </>
  );
}
