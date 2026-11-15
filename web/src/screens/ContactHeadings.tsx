import { useCallback, useEffect, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { hasRole, useMe, useSession } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { FormStatus, SubmitButton, type Status } from "../components/Form.tsx";
import { Icon } from "../components/Icon.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";

type Heading = components["schemas"]["ContactHeading"];

/** Officer-only screen to create, rename, archive, and delete contact headings. */
export function ContactHeadings() {
  useDocumentTitle("Rubros");
  const me = useMe();
  const { refresh } = useSession();

  const isOfficer = hasRole(me, "officer");

  const [headings, setHeadings] = useState<Heading[]>([]);
  const [loading, setLoading] = useState(isOfficer);
  const [newName, setNewName] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editError, setEditError] = useState<string | null>(null);

  const [status, setStatus] = useState<Status>(null);
  const [confirmArchive, setConfirmArchive] = useState<Heading | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<Heading | null>(null);

  const createRef = useRef<HTMLInputElement>(null);
  const editRef = useRef<HTMLInputElement>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const focusNext = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!focusNext.current) return;
    focusNext.current.focus();
    focusNext.current = null;
  });

  const handleAuthError = useCallback(
    (code: string) => {
      if (code === "mfa_required" || code === "unauthenticated") void refresh();
    },
    [refresh],
  );

  const load = useCallback(async () => {
    if (!isOfficer) return;
    setLoading(true);
    const res = await call(apiClient.GET("/contacts/headings"));
    setLoading(false);
    if (res.ok) setHeadings(res.data);
    else {
      handleAuthError(res.code);
      setStatus({ tone: "error", text: errorMessage(res.code) });
    }
  }, [handleAuthError, isOfficer]);

  useEffect(() => {
    void load();
  }, [load]);

  async function handleCreate() {
    if (!newName.trim()) {
      setCreateError("Escribí el nombre del rubro.");
      focusNext.current = createRef.current;
      return;
    }
    setBusy(true);
    setCreateError(null);
    setStatus(null);
    const res = await call(apiClient.POST("/contacts/headings", { body: { name: newName.trim() } }));
    setBusy(false);
    if (res.ok) {
      setNewName("");
      setStatus({ tone: "ok", text: `Se agregó el rubro «${res.data.name}».` });
      await load();
      return;
    }
    handleAuthError(res.code);
    const msg = errorMessage(res.code);
    if (res.code === "name_required" || res.code === "heading_exists") {
      setCreateError(msg);
      focusNext.current = createRef.current;
    } else {
      setStatus({ tone: "error", text: msg });
      focusNext.current = statusRef.current;
    }
  }

  async function handleRename(id: string) {
    if (!editName.trim()) {
      setEditError("Escribí el nuevo nombre del rubro.");
      focusNext.current = editRef.current;
      return;
    }
    setBusy(true);
    setEditError(null);
    setStatus(null);
    const res = await call(
      apiClient.PATCH("/contacts/headings/{id}", { params: { path: { id } }, body: { name: editName.trim() } }),
    );
    setBusy(false);
    if (res.ok) {
      setEditingId(null);
      setEditName("");
      setStatus({ tone: "ok", text: `Se renombró el rubro a «${res.data.name}».` });
      await load();
      return;
    }
    handleAuthError(res.code);
    const msg = errorMessage(res.code);
    if (res.code === "name_required" || res.code === "heading_exists") {
      setEditError(msg);
      focusNext.current = editRef.current;
    } else {
      setStatus({ tone: "error", text: msg });
      focusNext.current = statusRef.current;
    }
  }

  async function handleArchive(h: Heading) {
    setConfirmArchive(null);
    setBusy(true);
    setStatus(null);
    const res = await call(apiClient.POST("/contacts/headings/{id}/archive", { params: { path: { id: h.id } } }));
    setBusy(false);
    if (res.ok) {
      setStatus({ tone: "ok", text: `Se archivó «${h.name}».` });
      await load();
      return;
    }
    handleAuthError(res.code);
    setStatus({ tone: "error", text: errorMessage(res.code) });
    focusNext.current = statusRef.current;
  }

  async function handleDelete(h: Heading) {
    setConfirmDelete(null);
    setBusy(true);
    setStatus(null);
    const res = await call(apiClient.DELETE("/contacts/headings/{id}", { params: { path: { id: h.id } } }));
    setBusy(false);
    if (res.ok) {
      setStatus({ tone: "ok", text: `Se borró «${h.name}».` });
      await load();
      return;
    }
    handleAuthError(res.code);
    setStatus({ tone: "error", text: errorMessage(res.code) });
    focusNext.current = statusRef.current;
  }

  if (!isOfficer) {
    return (
      <>
        <header className="page-header">
          <PageTitle eyebrow="Contactos">Rubros</PageTitle>
        </header>
        <section className="banner" role="alert" aria-labelledby="no-access-title">
          <Icon name="alert" />
          <div className="banner__body">
            <h2 id="no-access-title">No tenés acceso a esta sección</h2>
            <p>Solo quienes integran la comisión directiva pueden manejar los rubros.</p>
          </div>
          <Link className="button button--secondary" to="/contactos">
            Volver a contactos
          </Link>
        </section>
      </>
    );
  }

  return (
    <>
      <header className="page-header">
        <div>
          <PageTitle eyebrow="Contactos">Rubros</PageTitle>
          <p className="page-header__school">
            Organizá los rubros del directorio. Si un rubro ya tiene contactos, archivalo en lugar de borrarlo.
          </p>
        </div>
        <div className="page-actions">
          <Link className="button button--secondary" to="/contactos">
            Volver a contactos
          </Link>
        </div>
      </header>

      <FormStatus status={status} ref={statusRef} />

      <section className="panel">
        <h2>Nuevo rubro</h2>
        <form
          noValidate
          aria-label="Nuevo rubro"
          onSubmit={(e) => {
            e.preventDefault();
            void handleCreate();
          }}
        >
          <div className="field">
            <label htmlFor="new-heading-name">Nombre del rubro</label>
            <input
              id="new-heading-name"
              ref={createRef}
              name="name"
              type="text"
              value={newName}
              aria-invalid={createError ? true : undefined}
              aria-describedby={createError ? "new-heading-error" : undefined}
              onChange={(e) => {
                setNewName(e.target.value);
                setCreateError(null);
              }}
            />
            {createError && (
              <p id="new-heading-error" className="error field__error" role="alert">
                {createError}
              </p>
            )}
          </div>
          <div className="panel__actions">
            <SubmitButton pending={busy}>
              {busy ? "Agregando…" : "Agregar rubro"}
            </SubmitButton>
          </div>
        </form>
      </section>

      <section className="panel">
        <h2>Listado de rubros</h2>
        {loading ? (
          <p className="loading" aria-busy="true">
            Cargando rubros…
          </p>
        ) : (
          <ul className="heading-list">
            {headings.map((h) => (
              <li key={h.id} className="heading-list__item" data-testid={`heading-${h.id}`}>
                {editingId === h.id ? (
                  <form
                    noValidate
                    aria-label={`Renombrar ${h.name}`}
                    className="heading-list__rename"
                    onSubmit={(e) => {
                      e.preventDefault();
                      void handleRename(h.id);
                    }}
                  >
                    <div className="field">
                      <label htmlFor={`edit-heading-${h.id}`}>Nuevo nombre</label>
                      <input
                        id={`edit-heading-${h.id}`}
                        ref={editRef}
                        type="text"
                        value={editName}
                        aria-invalid={editError ? true : undefined}
                        aria-describedby={editError ? `edit-heading-err-${h.id}` : undefined}
                        onChange={(e) => {
                          setEditName(e.target.value);
                          setEditError(null);
                        }}
                      />
                      {editError && (
                        <p id={`edit-heading-err-${h.id}`} className="error field__error" role="alert">
                          {editError}
                        </p>
                      )}
                    </div>
                    <div className="panel__actions">
                      <SubmitButton pending={busy}>
                        {busy ? "Guardando…" : "Guardar"}
                      </SubmitButton>
                      <button
                        type="button"
                        className="button button--secondary"
                        disabled={busy}
                        onClick={() => {
                          setEditingId(null);
                          setEditError(null);
                        }}
                      >
                        Cancelar
                      </button>
                    </div>
                  </form>
                ) : (
                  <>
                    <div className="heading-list__name">
                      <span>
                        <bdi>{h.name}</bdi>
                      </span>
                      {h.archived && <span className="badge">Archivado</span>}
                      {h.reserved && <span className="badge">Por defecto</span>}
                    </div>
                    {!h.reserved && (
                      <div className="panel__actions">
                        <button
                          type="button"
                          className="button button--secondary button--small"
                          disabled={busy}
                          onClick={() => {
                            setEditingId(h.id);
                            setEditName(h.name);
                            setEditError(null);
                            focusNext.current = editRef.current;
                          }}
                        >
                          Renombrar
                        </button>
                        {!h.archived && (
                          <button
                            type="button"
                            className="button button--secondary button--small"
                            disabled={busy}
                            onClick={() => setConfirmArchive(h)}
                          >
                            Archivar
                          </button>
                        )}
                        <button
                          type="button"
                          className="button button--secondary button--small"
                          disabled={busy}
                          onClick={() => setConfirmDelete(h)}
                        >
                          Borrar
                        </button>
                      </div>
                    )}
                  </>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      {confirmArchive && (
        <ConfirmDialog
          title="Archivar rubro"
          confirmLabel="Archivar"
          onConfirm={() => void handleArchive(confirmArchive)}
          onCancel={() => setConfirmArchive(null)}
        >
          <p>
            ¿Querés archivar el rubro <strong>{confirmArchive.name}</strong>? Los contactos existentes lo van a seguir
            mostrando, pero no se va a poder elegir en contactos nuevos.
          </p>
        </ConfirmDialog>
      )}

      {confirmDelete && (
        <ConfirmDialog
          title="Borrar rubro"
          confirmLabel="Borrar"
          onConfirm={() => void handleDelete(confirmDelete)}
          onCancel={() => setConfirmDelete(null)}
        >
          <p>
            ¿Querés borrar el rubro <strong>{confirmDelete.name}</strong>? Si ya tiene contactos asociados, primero vas a
            tener que archivarlo.
          </p>
        </ConfirmDialog>
      )}
    </>
  );
}
