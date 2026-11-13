import { useCallback, useEffect, useId, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { useSession } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { FormStatus, type Status } from "../components/Form.tsx";
import { Icon } from "../components/Icon.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { IssuedCodes, type IssuedMemberCode } from "./IssuedCodes.tsx";
import { RosterImport } from "./RosterImport.tsx";

type Member = components["schemas"]["RosterMember"];

/** Lowercase without accents, so "gomez" finds "Gómez". */
const fold = (text: string) => text.normalize("NFD").replace(/\p{M}/gu, "").toLowerCase();

function matches(member: Member, query: string): boolean {
  const text = fold(query.trim());
  const digits = query.replace(/\D/g, "");
  return !text || fold(member.fullName).includes(text) || (digits !== "" && member.documentNumber.includes(digits));
}

/** Spanish message for a failed roster request; a lost session or two-factor reloads the user so the guard can route. */
function useDescribeFailure() {
  const { refresh } = useSession();
  return useCallback(
    (code: string) => {
      if (code === "mfa_required" || code === "unauthenticated") void refresh();
      return errorMessage(code);
    },
    [refresh],
  );
}

/** /padron: import the roster and hand out access codes (school admins with two-factor). */
export function Roster() {
  useDocumentTitle("Padrón");
  const describeFailure = useDescribeFailure();
  const fileInput = useRef<HTMLInputElement>(null);
  const [members, setMembers] = useState<Member[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [codes, setCodes] = useState<IssuedMemberCode[]>([]);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState<string | null>(null); // a member id, or "bulk"
  const [confirming, setConfirming] = useState<Member | null>(null);

  const load = useCallback(async () => {
    setLoadError(null);
    const result = await call(apiClient.GET("/roster"));
    if (result.ok) setMembers(result.data);
    else setLoadError(describeFailure(result.code));
  }, [describeFailure]);

  useEffect(() => {
    void load();
  }, [load]);

  function show(issued: IssuedMemberCode[]) {
    const ids = new Set(issued.map((c) => c.memberId));
    // A new code revokes the previous one: never keep both on screen.
    setCodes((prev) => [...prev.filter((c) => !ids.has(c.memberId)), ...issued]);
    setMembers((prev) => prev?.map((m) => (ids.has(m.id) ? { ...m, hasActiveCode: true } : m)) ?? null);
  }

  async function issue(member: Member) {
    setConfirming(null);
    setBusy(member.id);
    setStatus(null);
    const result = await call(
      apiClient.POST("/roster/{memberId}/access-code", { params: { path: { memberId: member.id } } }),
    );
    setBusy(null);
    if (result.ok) return show([{ ...result.data, memberId: member.id, fullName: member.fullName, documentNumber: member.documentNumber }]);
    if (result.code === "member_has_account") {
      setMembers((prev) => prev?.map((m) => (m.id === member.id ? { ...m, hasAccount: true } : m)) ?? null);
    }
    setStatus({ tone: "error", text: describeFailure(result.code) });
  }

  async function issueMissing() {
    setBusy("bulk");
    setStatus(null);
    const result = await call(apiClient.POST("/roster/access-codes"));
    setBusy(null);
    if (!result.ok) return setStatus({ tone: "error", text: describeFailure(result.code) });
    if (result.data.length === 0) {
      return setStatus({ tone: "info", text: "Nadie necesita código: los alumnos sin cuenta ya tienen uno activo." });
    }
    show(result.data);
    setStatus({
      tone: "ok",
      text: result.data.length === 1 ? "Se generó 1 código." : `Se generaron ${result.data.length} códigos.`,
    });
  }

  const count = (pick: (m: Member) => boolean) => (members ? String(members.filter(pick).length) : "…");

  return (
    <>
      <header className="page-header">
        <PageTitle eyebrow="Escuela">Padrón</PageTitle>
        <div className="page-actions">
          <button type="button" className="button button--primary" onClick={() => fileInput.current?.click()}>
            <Icon name="upload" />
            Importar CSV
          </button>
          <button
            type="button"
            className="button button--primary"
            disabled={busy !== null}
            aria-busy={busy === "bulk" || undefined}
            onClick={() => void issueMissing()}
          >
            Generar códigos faltantes
          </button>
        </div>
      </header>

      <dl className="stats">
        <div>
          <dt>Alumnos</dt>
          <dd>{count(() => true)}</dd>
        </div>
        <div>
          <dt>Con cuenta</dt>
          <dd>{count((m) => m.hasAccount)}</dd>
        </div>
        <div>
          <dt>Con código activo</dt>
          <dd>{count((m) => m.hasActiveCode)}</dd>
        </div>
      </dl>

      <FormStatus status={status} />
      {codes.length > 0 && <IssuedCodes codes={codes} onHide={() => setCodes([])} />}

      <RosterImport inputRef={fileInput} onImported={() => void load()} describeFailure={describeFailure} />

      {loadError !== null ? (
        <section className="panel">
          <FormStatus status={{ tone: "error", text: loadError }} />
          <button type="button" className="button button--secondary" onClick={() => void load()}>
            Reintentar
          </button>
        </section>
      ) : members === null ? (
        <p className="muted" aria-busy="true">
          Cargando el padrón…
        </p>
      ) : (
        <MemberTable members={members} busy={busy} onIssue={(m) => (m.hasActiveCode ? setConfirming(m) : void issue(m))} />
      )}

      {confirming && (
        <ConfirmDialog
          title={`¿Generar un código nuevo para ${confirming.fullName}?`}
          confirmLabel="Generar código nuevo"
          onConfirm={() => void issue(confirming)}
          onCancel={() => setConfirming(null)}
        >
          <p>
            {`${confirming.fullName} ya tiene un código activo. El código anterior va a dejar de funcionar: si ya se lo entregaste, dale el nuevo.`}
          </p>
        </ConfirmDialog>
      )}
    </>
  );
}

type TableProps = { members: Member[]; busy: string | null; onIssue: (member: Member) => void };

function MemberTable({ members, busy, onIssue }: TableProps) {
  const id = useId();
  const [query, setQuery] = useState("");
  const visible = members.filter((m) => matches(m, query));

  if (members.length === 0) {
    return (
      <section className="panel">
        <p className="muted">Todavía no hay alumnos. Importá el padrón desde un CSV para empezar.</p>
      </section>
    );
  }

  return (
    <section className="panel" aria-labelledby={`${id}-title`}>
      <div className="panel__header">
        <h2 id={`${id}-title`}>Alumnos</h2>
        <div className="field field--inline">
          <label htmlFor={`${id}-filter`}>Buscar por nombre o DNI</label>
          <input
            id={`${id}-filter`}
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
        </div>
      </div>
      <p className="muted small" role="status">
        {query.trim() ? `${visible.length} de ${members.length} alumnos` : ""}
      </p>
      <div className="table-wrap" tabIndex={0} role="region" aria-labelledby={`${id}-caption`}>
        <table className="data-table">
          <caption id={`${id}-caption`} className="visually-hidden">
            Alumnos del padrón
          </caption>
          <thead>
            <tr>
              <th scope="col">Alumno</th>
              <th scope="col">DNI</th>
              <th scope="col">Cuenta</th>
              <th scope="col">Código</th>
              <th scope="col">
                <span className="visually-hidden">Acciones</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {visible.map((m) => (
              <tr key={m.id}>
                <th scope="row">
                  <bdi>{m.fullName}</bdi>
                </th>
                <td data-label="DNI" className="mono">
                  {m.documentNumber}
                </td>
                <td data-label="Cuenta">
                  <span className="badge" data-tone={m.hasAccount ? "ok" : undefined}>
                    {m.hasAccount ? "Con cuenta" : "Sin cuenta"}
                  </span>
                </td>
                <td data-label="Código">
                  <span className="badge" data-tone={m.hasActiveCode ? "ok" : undefined}>
                    {m.hasActiveCode ? "Código activo" : "Sin código"}
                  </span>
                </td>
                <td className="data-table__action">
                  <button
                    type="button"
                    className="button button--secondary button--small"
                    disabled={m.hasAccount || busy !== null}
                    aria-busy={busy === m.id || undefined}
                    aria-describedby={m.hasAccount ? `${id}-${m.id}-why` : undefined}
                    onClick={() => onIssue(m)}
                  >
                    Generar código
                  </button>
                  {m.hasAccount && (
                    <p id={`${id}-${m.id}-why`} className="muted small">
                      Ya creó su cuenta: no necesita código.
                    </p>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {visible.length === 0 && <p className="muted">{`No hay alumnos que coincidan con «${query.trim()}».`}</p>}
    </section>
  );
}
