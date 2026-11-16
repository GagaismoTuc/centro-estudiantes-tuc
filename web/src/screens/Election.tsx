import { useCallback, useEffect, useId, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { useSession } from "../auth/session.tsx";
import { FieldError, FormStatus, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import {
  formatArgentina,
  loadCommissionStatus,
  stateLabel,
  type CommissionStatus,
  type Election as ElectionData,
} from "./electionShared.ts";

type CommissionMember = components["schemas"]["CommissionMember"];

/** The current election and its Electoral commission ("Centro de estudiantes"), for every school member. */
export function Election() {
  useDocumentTitle("Elección");
  const { refresh } = useSession();
  const [commission, setCommission] = useState<CommissionStatus | null>(null);
  // undefined while loading, null when there is no election in progress.
  const [election, setElection] = useState<ElectionData | null | undefined>(undefined);
  const [status, setStatus] = useState<Status>(null);

  const failed = useCallback(
    (code: string) => {
      if (code === "mfa_required" || code === "unauthenticated") void refresh();
      setStatus({ tone: "error", text: errorMessage(code) });
    },
    [refresh],
  );

  const load = useCallback(async () => {
    // The latest election, so an annulled one stays visible with its reason.
    const [c, e] = await Promise.all([loadCommissionStatus(), call(apiClient.GET("/elections/latest"))]);
    if (c.ok) setCommission(c.data);
    else failed(c.code);
    if (e.ok) setElection(e.data);
    else if (e.code === "not_found") setElection(null);
    else failed(e.code);
  }, [failed]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <>
      <header className="page-header">
        <PageTitle eyebrow="Centro">Elección</PageTitle>
      </header>
      <FormStatus status={status} />
      {election === null && (
        <section className="panel">
          <p>Todavía no hay una elección en curso.</p>
        </section>
      )}
      {election && <ElectionDetails election={election} />}
      {(election === null || election?.state === "annulled") && commission?.commission && (
        <section className="panel" aria-labelledby="commission-title">
          <h2 id="commission-title">Centro de estudiantes</h2>
          <MemberList members={commission.commission.members} />
        </section>
      )}
      {commission?.canDesignate && (
        <Designation current={commission.commission?.members ?? []} onSaved={load} onFailed={failed} />
      )}
    </>
  );
}

function MemberList({ members }: { members: CommissionMember[] }) {
  if (members.length === 0) return <p className="muted">Todavía no se designó el Centro de estudiantes.</p>;
  return (
    <ul className="plain-list" aria-label="Integrantes del Centro de estudiantes">
      {members.map((m) => (
        <li key={m.rosterMemberId}>
          <bdi>{m.fullName}</bdi>
        </li>
      ))}
    </ul>
  );
}

function ElectionDetails({ election }: { election: ElectionData }) {
  const id = useId();
  return (
    <section className="panel" aria-labelledby={`${id}-name`}>
      <h2 id={`${id}-name`}>
        <bdi>{election.name}</bdi>
      </h2>
      {election.annulment && (
        <p>
          <strong>Anulada:</strong> <bdi>{election.annulment.reason}</bdi>
        </p>
      )}
      <dl className="details">
        <div>
          <dt>Estado</dt>
          <dd>{stateLabel[election.state]}</dd>
        </div>
        <div>
          <dt>Modo de votación</dt>
          <dd>Voto secreto</dd>
        </div>
        <div>
          <dt>Inicio de la presentación de listas</dt>
          <dd>{formatArgentina(election.submissionStartsAt)}</dd>
        </div>
        <div>
          <dt>Fin de la presentación de listas</dt>
          <dd>{formatArgentina(election.submissionEndsAt)}</dd>
        </div>
        <div>
          <dt>Inicio de la votación</dt>
          <dd>{formatArgentina(election.startsAt)}</dd>
        </div>
        <div>
          <dt>Fin de la votación</dt>
          <dd>{formatArgentina(election.endsAt)}</dd>
        </div>
      </dl>
      <p className="muted small">Los horarios son de Argentina.</p>
      <h3 id={`${id}-positions`}>Cargos</h3>
      <ol className="plain-list" aria-labelledby={`${id}-positions`}>
        {election.positions.map((position) => (
          <li key={position}>
            <bdi>{position}</bdi>
          </li>
        ))}
      </ol>
      <h3>Centro de estudiantes</h3>
      <MemberList members={election.commission} />
    </section>
  );
}

/** Officers (or the School admin when there are none) pick the members of the Electoral commission. */
function Designation({
  current,
  onSaved,
  onFailed,
}: {
  current: CommissionMember[];
  onSaved: () => Promise<void>;
  onFailed: (code: string) => void;
}) {
  const id = useId();
  const { refresh } = useSession();
  const [candidates, setCandidates] = useState<CommissionMember[]>([]);
  const [selected, setSelected] = useState<Set<string> | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const statusRef = useRef<HTMLDivElement>(null);
  const firstBoxRef = useRef<HTMLInputElement>(null);
  const currentIds = current.map((m) => m.rosterMemberId).join(",");

  useEffect(() => {
    void (async () => {
      const result = await call(apiClient.GET("/electoral-commission/candidates"));
      if (result.ok) setCandidates(result.data);
      else onFailed(result.code);
    })();
  }, [onFailed]);

  // Start from the members already designated, until the user picks.
  const picked = selected ?? new Set(currentIds ? currentIds.split(",") : []);

  function toggle(memberId: string) {
    const next = new Set(picked);
    if (next.has(memberId)) next.delete(memberId);
    else next.add(memberId);
    setSelected(next);
    setFieldError(null);
  }

  async function save() {
    setStatus(null);
    setFieldError(null);
    const rosterMemberIds = candidates.map((c) => c.rosterMemberId).filter((memberId) => picked.has(memberId));
    if (rosterMemberIds.length === 0) {
      setFieldError(errorMessage("empty_commission"));
      firstBoxRef.current?.focus();
      return;
    }
    setBusy(true);
    const result = await call(apiClient.PUT("/electoral-commission", { body: { rosterMemberIds } }));
    setBusy(false);
    if (result.ok) {
      setStatus({ tone: "ok", text: "Se guardó el Centro de estudiantes." });
      await onSaved();
      return;
    }
    if (result.code === "empty_commission" || result.code === "no_account") {
      setFieldError(errorMessage(result.code));
      firstBoxRef.current?.focus();
      return;
    }
    if (result.code === "mfa_required" || result.code === "unauthenticated") void refresh();
    setStatus({ tone: "error", text: errorMessage(result.code) });
    statusRef.current?.focus();
  }

  return (
    <section className="panel" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`}>Designá el Centro de estudiantes</h2>
      <p className="muted small">
        Quienes integran el Centro de estudiantes crean y organizan la elección. Tienen que tener cuenta.
      </p>
      <FormStatus status={status} ref={statusRef} />
      <form
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <fieldset className="choices" aria-describedby={fieldError ? `${id}-error` : undefined}>
          <legend>Elegí los integrantes</legend>
          {candidates.length === 0 && <p className="muted">Todavía no hay alumnos del padrón con cuenta.</p>}
          {candidates.map((c, i) => (
            <div className="check" key={c.rosterMemberId}>
              <input
                id={`${id}-${c.rosterMemberId}`}
                ref={i === 0 ? firstBoxRef : undefined}
                type="checkbox"
                checked={picked.has(c.rosterMemberId)}
                aria-invalid={fieldError ? true : undefined}
                aria-describedby={fieldError ? `${id}-error` : undefined}
                onChange={() => toggle(c.rosterMemberId)}
              />
              <label htmlFor={`${id}-${c.rosterMemberId}`}>
                <bdi>{c.fullName}</bdi>
              </label>
            </div>
          ))}
          {fieldError && <FieldError id={`${id}-error`}>{fieldError}</FieldError>}
        </fieldset>
        <button type="submit" className="button button--primary" disabled={busy || candidates.length === 0} aria-busy={busy || undefined}>
          Guardar integrantes
        </button>
      </form>
    </section>
  );
}
