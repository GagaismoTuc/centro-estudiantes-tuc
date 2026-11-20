import { useCallback, useEffect, useId, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { useSession } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { Field, FieldError, FormStatus, useFormErrors, type Check, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";
import { ListCard, ListForm } from "./ElectionLists.tsx";
import {
  DEFAULT_POSITIONS,
  fromLocalInput,
  loadCommissionStatus,
  toLocalInput,
  type CommissionStatus,
  type Election,
  type ElectionList,
} from "./electionShared.ts";
import { SecretBallotGuarantee } from "./Vote.tsx";

/** The "Centro de estudiantes" section: members of the Electoral commission create and edit the draft,
 *  and review or load election lists during the submission period. */
export function CommissionPanel() {
  useDocumentTitle("Centro de estudiantes");
  const { refresh } = useSession();
  const [commission, setCommission] = useState<CommissionStatus | null>(null);
  // undefined while loading, null when there is no election in progress.
  const [election, setElection] = useState<Election | null | undefined>(undefined);
  const [lists, setLists] = useState<ElectionList[] | null>(null);
  const [editingList, setEditingList] = useState<ElectionList | null>(null);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<Status>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const focusStatus = useRef(false);

  useEffect(() => {
    if (focusStatus.current && status?.tone === "error") {
      statusRef.current?.focus();
      focusStatus.current = false;
    }
  }, [status]);

  const load = useCallback(async () => {
    const [c, e] = await Promise.all([loadCommissionStatus(), call(apiClient.GET("/elections/current"))]);
    const failed = !c.ok ? c.code : !e.ok && e.code !== "not_found" ? e.code : null;
    if (failed) {
      if (failed === "mfa_required" || failed === "unauthenticated") void refresh();
      setStatus({ tone: "error", text: errorMessage(failed) });
      return;
    }
    if (c.ok) setCommission(c.data);
    const nextElection = e.ok ? e.data : null;
    setElection(nextElection);
    if (c.ok && c.data.isMember && nextElection && nextElection.state !== "draft") {
      const listsRes = await call(
        apiClient.GET("/elections/{electionId}/lists", { params: { path: { electionId: nextElection.id } } }),
      );
      if (listsRes.ok) setLists(listsRes.data.lists);
      else {
        if (listsRes.code === "mfa_required" || listsRes.code === "unauthenticated") void refresh();
        setStatus({ tone: "error", text: errorMessage(listsRes.code) });
      }
    } else {
      setLists(null);
    }
  }, [refresh]);

  useEffect(() => {
    void load();
  }, [load]);

  async function approve(listId: string) {
    if (!election) return;
    setStatus(null);
    setBusy(true);
    const result = await call(
      apiClient.POST("/elections/{electionId}/lists/{listId}/approve", {
        params: { path: { electionId: election.id, listId } },
      }),
    );
    setBusy(false);
    if (result.ok) {
      setStatus({ tone: "ok", text: "Se aprobó la lista." });
      await load();
      return;
    }
    if (result.code === "mfa_required" || result.code === "unauthenticated") void refresh();
    focusStatus.current = true;
    setStatus({ tone: "error", text: errorMessage(result.code, "election-list") });
  }

  return (
    <>
      <header className="page-header">
        <PageTitle eyebrow="Elección">Centro de estudiantes</PageTitle>
      </header>
      <FormStatus status={status} ref={statusRef} />
      {commission && !commission.isMember && (
        <section className="panel">
          <p>Esta sección es para quienes integran el Centro de estudiantes.</p>
        </section>
      )}
      {commission?.isMember && election !== undefined && (election === null || election.state === "draft") && (
        <DraftForm election={election} onSaved={setElection} />
      )}
      {commission?.isMember && election && election.state !== "draft" && (
        <>
          <section className="panel">
            <p>{errorMessage("election_not_draft")}</p>
            <p>
              <Link to="/eleccion">Ver la elección</Link>
            </p>
          </section>
          {lists && (
            <section className="panel" aria-labelledby="commission-lists-title">
              <h2 id="commission-lists-title">Listas presentadas</h2>
              {lists.length === 0 ? (
                <p className="muted">Todavía no se presentaron listas.</p>
              ) : (
                <div className="election-lists">
                  {lists.map((l) => (
                    <ListCard
                      key={l.id}
                      list={l}
                      busy={busy}
                      canEdit={election.state === "submission" && l.status === "pending"}
                      canReview={election.state === "submission" && l.status === "pending"}
                      onEdit={() => setEditingList(l)}
                      onApprove={() => void approve(l.id)}
                      onRejected={async () => {
                        setStatus({ tone: "ok", text: "Se rechazó la lista." });
                        await load();
                      }}
                    />
                  ))}
                </div>
              )}
            </section>
          )}
          {election.state === "submission" && editingList && (
            <ListForm
              mode="edit"
              election={election}
              initial={editingList}
              onSaved={async (msg) => {
                setEditingList(null);
                setStatus({ tone: "ok", text: msg });
                await load();
              }}
              onCancel={() => setEditingList(null)}
            />
          )}
          {election.state === "submission" && (
            <ListForm
              mode="commission"
              election={election}
              onSaved={async (msg) => {
                setStatus({ tone: "ok", text: msg });
                await load();
              }}
            />
          )}
        </>
      )}
      {commission?.isMember && election && <SecretBallotGuarantee />}
      {commission?.isMember && election && annullable.includes(election.state) && (
        <Annulment
          election={election}
          onDone={async (next) => {
            setStatus(next);
            await load();
            // The dialog and the section that opened it are gone: the outcome takes the focus.
            statusRef.current?.focus();
          }}
        />
      )}
    </>
  );
}

// Before the end of the voting the commission may annul the election; from then on its result is published.
const annullable: Election["state"][] = ["draft", "submission", "scheduled", "open"];

/** "Anular elección": a confirmation that asks for the public reason. */
export function Annulment({ election, onDone, isVeedor = false }: { election: Election; onDone: (status: Status) => Promise<void>; isVeedor?: boolean }) {
  const id = useId();
  const { refresh } = useSession();
  const [asking, setAsking] = useState(false);
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const fieldRef = useRef<HTMLTextAreaElement>(null);

  function invalid(code: string) {
    setError(errorMessage(code));
    fieldRef.current?.focus();
  }

  async function annul() {
    if (busy) return;
    if (!reason.trim()) return invalid("reason_required");
    setBusy(true);
    const result = await call(
      apiClient.POST("/elections/{electionId}/annulment", { params: { path: { electionId: election.id } }, body: { reason } }),
    );
    setBusy(false);
    if (!result.ok && (result.code === "reason_required" || result.code === "reason_too_long")) return invalid(result.code);
    setAsking(false);
    if (!result.ok && (result.code === "mfa_required" || result.code === "unauthenticated")) void refresh();
    await onDone(
      result.ok
        ? { tone: "ok", text: "Se anuló la elección. Todos van a ver el motivo." }
        : { tone: "error", text: errorMessage(result.code) },
    );
  }

  return (
    <section className="panel" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`}>Anular la elección</h2>
      {isVeedor && <p>{"Anul\u00e1 la elecci\u00f3n solo si hace falta; el motivo lo van a ver todos."}</p>}
      <p className="muted small">
        Es definitivo: la elección no publica resultado, no acepta votos y no se vuelve a abrir. El Centro de estudiantes
        termina su mandato.
      </p>
      <button
        type="button"
        className="button button--secondary"
        onClick={() => {
          setReason("");
          setError(null);
          setAsking(true);
        }}
      >
        Anular elección
      </button>
      {asking && (
        <ConfirmDialog
          title="¿Anular la elección?"
          confirmLabel="Anular definitivamente"
          onConfirm={() => void annul()}
          onCancel={() => setAsking(false)}
        >
          <div className="field">
            <label htmlFor={`${id}-reason`}>Motivo</label>
            <p className="field__hint" id={`${id}-hint`}>
              Escribí el motivo; lo van a ver todos.
            </p>
            <textarea
              id={`${id}-reason`}
              ref={fieldRef}
              name="reason"
              value={reason}
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? `${id}-hint ${id}-error` : `${id}-hint`}
              onChange={(event) => {
                setReason(event.target.value);
                setError(null);
              }}
            />
            {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
          </div>
        </ConfirmDialog>
      )}
    </section>
  );
}

type Position = { key: number; name: string };
type Dates = { submissionStartsAt: string; submissionEndsAt: string; startsAt: string; endsAt: string };

const dateFields: { name: keyof Dates; label: string }[] = [
  { name: "submissionStartsAt", label: "Inicio de la presentación de listas" },
  { name: "submissionEndsAt", label: "Fin de la presentación de listas" },
  { name: "startsAt", label: "Inicio de la votación" },
  { name: "endsAt", label: "Fin de la votación" },
];

const pickDate: Check = (v) => (v ? null : "Elegí la fecha y la hora.");
const checks: Record<string, Check> = {
  name: (v) => (v.trim() ? null : "Escribí el nombre de la elección."),
  submissionStartsAt: pickDate,
  submissionEndsAt: pickDate,
  startsAt: pickDate,
  endsAt: pickDate,
};

let nextKey = 0;
const toPositions = (names: string[]): Position[] => names.map((name) => ({ key: nextKey++, name }));

function DraftForm({ election, onSaved }: { election: Election | null; onSaved: (election: Election) => void }) {
  const { refresh } = useSession();
  const form = useFormErrors();
  const [name, setName] = useState(election?.name ?? "");
  const [dates, setDates] = useState<Dates>(() => ({
    submissionStartsAt: election ? toLocalInput(election.submissionStartsAt) : "",
    submissionEndsAt: election ? toLocalInput(election.submissionEndsAt) : "",
    startsAt: election ? toLocalInput(election.startsAt) : "",
    endsAt: election ? toLocalInput(election.endsAt) : "",
  }));
  const [positions, setPositions] = useState<Position[]>(() => toPositions(election?.positions ?? DEFAULT_POSITIONS));
  const [added, setAdded] = useState<number | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const addRef = useRef<HTMLButtonElement>(null);

  function rename(key: number, value: string) {
    setPositions((list) => list.map((p) => (p.key === key ? { ...p, name: value } : p)));
  }
  function remove(key: number) {
    setPositions((list) => list.filter((p) => p.key !== key));
    // The removed row took its button with it: keep focus inside the list.
    addRef.current?.focus();
  }
  function add() {
    const [position] = toPositions([""]);
    setPositions((list) => [...list, position!]);
    setAdded(position!.key);
  }

  async function submit() {
    setStatus(null);
    if (!form.validate(checks)) return;
    setBusy(true);
    const body = {
      name,
      mode: "secret" as const,
      submissionStartsAt: fromLocalInput(dates.submissionStartsAt),
      submissionEndsAt: fromLocalInput(dates.submissionEndsAt),
      startsAt: fromLocalInput(dates.startsAt),
      endsAt: fromLocalInput(dates.endsAt),
      positions: positions.map((p) => p.name.trim()),
    };
    const result = election
      ? await call(apiClient.PUT("/elections/{electionId}", { params: { path: { electionId: election.id } }, body }))
      : await call(apiClient.POST("/elections", { body }));
    setBusy(false);
    if (result.ok) {
      setStatus({ tone: "ok", text: election ? "Se guardaron los cambios." : "Se creó la elección." });
      onSaved(result.data);
      return;
    }
    if (result.code === "mfa_required" || result.code === "unauthenticated") void refresh();
    setStatus({ tone: "error", text: errorMessage(result.code, "election") });
    form.failed(result.code);
  }

  return (
    <section className="panel" aria-labelledby="draft-title">
      <h2 id="draft-title">{election ? "Editá la elección" : "Creá la elección"}</h2>
      <p className="muted small">Las fechas y horas son de Argentina. Podés editar la elección mientras sea un borrador.</p>
      <form
        {...form.formProps}
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <FormStatus status={status} {...form.statusProps} />
        <Field
          label="Nombre de la elección"
          {...form.field("name")}
          value={name}
          maxLength={120}
          required
          autoComplete="off"
          onChange={(event) => setName(event.target.value)}
        />
        <div className="field-grid">
          {dateFields.map((d) => (
            <Field
              key={d.name}
              label={d.label}
              type="datetime-local"
              required
              {...form.field(d.name)}
              value={dates[d.name]}
              onChange={(event) => setDates((current) => ({ ...current, [d.name]: event.target.value }))}
            />
          ))}
        </div>
        <fieldset className="choices">
          <legend>Modo de votación</legend>
          <div className="check">
            <input id="mode-secret" name="mode" type="radio" value="secret" checked readOnly />
            <label htmlFor="mode-secret">Voto secreto</label>
          </div>
          <p className="field__hint">Por ahora es el único modo.</p>
        </fieldset>
        <fieldset className="choices">
          <legend>Cargos</legend>
          <ol className="positions">
            {positions.map((p, i) => (
              <li key={p.key} className="positions__row">
                <Field
                  label={`Cargo ${i + 1}`}
                  {...form.field("position")}
                  value={p.name}
                  autoComplete="off"
                  autoFocus={p.key === added}
                  onChange={(event) => rename(p.key, event.target.value)}
                />
                <button
                  type="button"
                  className="button button--secondary"
                  aria-label={`Quitar el cargo ${p.name.trim() || i + 1}`}
                  disabled={positions.length === 1}
                  onClick={() => remove(p.key)}
                >
                  Quitar
                </button>
              </li>
            ))}
          </ol>
          <div>
            <button ref={addRef} type="button" className="button button--secondary" onClick={add}>
              Agregar cargo
            </button>
          </div>
        </fieldset>
        <div>
          <button type="submit" className="button button--primary" disabled={busy} aria-busy={busy || undefined}>
            {election ? "Guardar cambios" : "Crear elección"}
          </button>
        </div>
      </form>
    </section>
  );
}
