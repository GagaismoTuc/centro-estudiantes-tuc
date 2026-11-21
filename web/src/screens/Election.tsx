import { useCallback, useEffect, useId, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { hasRole, useMe, useSession } from "../auth/session.tsx";
import { Annulment } from "./CommissionPanel.tsx";
import { FieldError, FormStatus, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { ListCard, ListForm } from "./ElectionLists.tsx";
import { formatLongDateTime } from "../lib/datetime.ts";
import {
  loadCommissionStatus,
  stateLabel,
  type CommissionStatus,
  type Election as ElectionData,
  type ElectionList,
  type ElectionListsResponse,
} from "./electionShared.ts";
import { ResultsPanel } from "./Results.tsx";
import { SecretBallotGuarantee, VotePanel } from "./Vote.tsx";

type CommissionMember = components["schemas"]["CommissionMember"];

/** The current election and its Electoral commission ("Centro de estudiantes"), for every school member. */
export function Election() {
  useDocumentTitle("Elección");
  const { refresh } = useSession();
  const isVeedor = hasRole(useMe(), "veedor");
  const [commission, setCommission] = useState<CommissionStatus | null>(null);
  // undefined while loading, null when there is no election in progress.
  const [election, setElection] = useState<ElectionData | null | undefined>(undefined);
  const [listsData, setListsData] = useState<ElectionListsResponse | null>(null);
  const [editingList, setEditingList] = useState<ElectionList | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const statusRef = useRef<HTMLDivElement>(null);

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
    if (e.ok) {
      setElection(e.data);
      if (e.data.state !== "draft" && e.data.state !== "annulled") {
        const listsRes = await call(
          apiClient.GET("/elections/{electionId}/lists", { params: { path: { electionId: e.data.id } } }),
        );
        if (listsRes.ok) setListsData(listsRes.data);
        else failed(listsRes.code);
      } else {
        setListsData(null);
      }
    } else if (e.code === "not_found") {
      setElection(null);
      setListsData(null);
    } else {
      failed(e.code);
    }
  }, [failed]);

  useEffect(() => {
    void load();
  }, [load]);

  const approvedLists = listsData?.lists.filter((l) => l.status === "approved") ?? [];
  const myLists = listsData?.rosterMemberId
    ? listsData.lists.filter((l) => l.representativeRosterMemberId === listsData.rosterMemberId)
    : [];

  return (
    <>
      <header className="page-header">
        <PageTitle eyebrow="Centro">Elección</PageTitle>
      </header>
      <FormStatus status={status} ref={statusRef} />
      {election === null && (
        <section className="panel">
          <p>Todavía no hay una elección en curso.</p>
        </section>
      )}
      {election && <ElectionDetails election={election} />}
      {election?.state === "open" && listsData && <VotePanel election={election} lists={approvedLists} canVote={!isVeedor} />}
      {election?.state === "closed" && <ResultsPanel electionId={election.id} />}
      {election && <SecretBallotGuarantee />}
      {election && election.state !== "draft" && election.state !== "annulled" && listsData && (
        <section className="panel" aria-labelledby="approved-lists-title">
          <h2 id="approved-lists-title">Listas aprobadas</h2>
          {approvedLists.length === 0 ? (
            <p className="muted">Todavía no hay listas aprobadas.</p>
          ) : (
            <div className="election-lists">
              {approvedLists.map((l) => (
                <ListCard key={l.id} list={l} />
              ))}
            </div>
          )}
        </section>
      )}
      {!isVeedor && election && election.state !== "annulled" && myLists.length > 0 && (
        <section className="panel" aria-labelledby="my-lists-title">
          <h2 id="my-lists-title">Mis listas</h2>
          <div className="election-lists">
            {myLists.map((l) => (
              <ListCard
                key={l.id}
                list={l}
                canEdit={election.state === "submission" && l.status === "pending"}
                onEdit={() => setEditingList(l)}
              />
            ))}
          </div>
        </section>
      )}
      {!isVeedor && election && election.state === "submission" && listsData?.rosterMemberId && (
        <ListForm
          mode={editingList ? "edit" : "submit"}
          election={election}
          initial={editingList ?? undefined}
          onSaved={async (msg) => {
            setEditingList(null);
            setStatus({ tone: "ok", text: msg });
            await load();
          }}
          onCancel={() => setEditingList(null)}
        />
      )}
      {(election === null || election?.state === "annulled") && commission?.commission && (
        <section className="panel" aria-labelledby="commission-title">
          <h2 id="commission-title">Centro de estudiantes</h2>
          <MemberList members={commission.commission.members} />
        </section>
      )}
      {isVeedor && election && ["draft", "submission", "scheduled", "open"].includes(election.state) && (
        <Annulment isVeedor election={election} onDone={async (next) => { setStatus(next); await load(); statusRef.current?.focus(); }} />
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
          <strong>{election.annulment.annulledBy === "veedor" ? "Anulada por el Veedor:" : "Anulada:"}</strong> <bdi>{election.annulment.reason}</bdi>
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
          <dd>{formatLongDateTime(election.submissionStartsAt)}</dd>
        </div>
        <div>
          <dt>Fin de la presentación de listas</dt>
          <dd>{formatLongDateTime(election.submissionEndsAt)}</dd>
        </div>
        <div>
          <dt>Inicio de la votación</dt>
          <dd>{formatLongDateTime(election.startsAt)}</dd>
        </div>
        <div>
          <dt>Fin de la votación</dt>
          <dd>{formatLongDateTime(election.endsAt)}</dd>
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
