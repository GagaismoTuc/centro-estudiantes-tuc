import { useEffect, useId, useMemo, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { useSession } from "../auth/session.tsx";
import { FieldError, FormStatus, type Status } from "../components/Form.tsx";
import { listStatusLabel, type CommissionMember, type Election, type ElectionList } from "./electionShared.ts";

export function ListCard({
  list,
  canEdit,
  canReview,
  busy,
  onEdit,
  onApprove,
  onRejected,
}: {
  list: ElectionList;
  canEdit?: boolean;
  canReview?: boolean;
  busy?: boolean;
  onEdit?: () => void;
  onApprove?: () => void;
  onRejected?: () => Promise<void>;
}) {
  const titleId = useId();
  const [rejecting, setRejecting] = useState(false);
  const badgeTone = list.status === "approved" ? "ok" : list.status === "rejected" ? "error" : undefined;

  return (
    <article className="list-card" aria-labelledby={titleId}>
      <div className="list-card__header">
        <h3 id={titleId} className="list-card__title">
          <bdi>{list.name}</bdi>
        </h3>
        <span className="badge" data-tone={badgeTone}>
          {listStatusLabel[list.status]}
        </span>
      </div>
      <p className="muted small">
        Apoderado: <bdi>{list.representativeFullName}</bdi>
      </p>
      <dl className="details">
        {list.candidates.map((c) => (
          <div key={c.position}>
            <dt>
              <bdi>{c.position}</bdi>
            </dt>
            <dd>
              <bdi>{c.fullName}</bdi>
            </dd>
          </div>
        ))}
      </dl>
      {list.status === "rejected" && list.rejectionReason && (
        <p className="list-card__reason">
          <strong>Motivo del rechazo:</strong> <bdi>{list.rejectionReason}</bdi>
        </p>
      )}
      {(canEdit || canReview) && !rejecting && (
        <div className="list-card__actions">
          {canEdit && onEdit && (
            <button type="button" className="button button--secondary button--small" disabled={busy} aria-label={`Editar lista ${list.name}`} onClick={onEdit}>
              Editar
            </button>
          )}
          {canReview && onApprove && (
            <button type="button" className="button button--primary button--small" disabled={busy} aria-label={`Aprobar ${list.name}`} onClick={onApprove}>
              Aprobar
            </button>
          )}
          {canReview && onRejected && (
            <button type="button" className="button button--secondary button--small" disabled={busy} aria-label={`Rechazar ${list.name}`} onClick={() => setRejecting(true)}>
              Rechazar
            </button>
          )}
        </div>
      )}
      {canReview && rejecting && onRejected && (
        <RejectListForm
          electionId={list.electionId}
          listId={list.id}
          onCancel={() => setRejecting(false)}
          onRejected={async () => {
            setRejecting(false);
            await onRejected();
          }}
        />
      )}
    </article>
  );
}

function RejectListForm({
  electionId,
  listId,
  onCancel,
  onRejected,
}: {
  electionId: string;
  listId: string;
  onCancel: () => void;
  onRejected: () => Promise<void>;
}) {
  const id = useId();
  const { refresh } = useSession();
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const reasonRef = useRef<HTMLTextAreaElement>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const focusNext = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!focusNext.current) return;
    focusNext.current.focus();
    focusNext.current = null;
  });

  async function submit() {
    setStatus(null);
    const trimmed = reason.trim();
    if (!trimmed || trimmed.length > 500) {
      setError(errorMessage("reason_required", "election-list"));
      focusNext.current = reasonRef.current;
      reasonRef.current?.focus();
      return;
    }
    setError(null);
    setBusy(true);
    const result = await call(
      apiClient.POST("/elections/{electionId}/lists/{listId}/reject", {
        params: { path: { electionId, listId } },
        body: { reason: trimmed },
      }),
    );
    setBusy(false);
    if (result.ok) {
      await onRejected();
      return;
    }
    if (result.code === "mfa_required" || result.code === "unauthenticated") void refresh();
    const msg = errorMessage(result.code, "election-list");
    if (result.code === "reason_required") {
      setError(msg);
      focusNext.current = reasonRef.current;
      return;
    }
    setStatus({ tone: "error", text: msg });
    focusNext.current = statusRef.current;
  }

  return (
    <form
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <FormStatus status={status} ref={statusRef} />
      <div className="field">
        <label htmlFor={`${id}-reason`}>Motivo del rechazo</label>
        <textarea
          id={`${id}-reason`}
          ref={reasonRef}
          name="reason"
          maxLength={500}
          required
          value={reason}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? `${id}-reason-error` : undefined}
          onChange={(event) => {
            setReason(event.target.value);
            setError(null);
          }}
        />
        {error && <FieldError id={`${id}-reason-error`}>{error}</FieldError>}
      </div>
      <div className="list-card__actions">
        <button type="submit" className="button button--primary button--small" disabled={busy} aria-busy={busy || undefined}>
          Confirmar rechazo
        </button>
        <button type="button" className="button button--secondary button--small" disabled={busy} onClick={onCancel}>
          Cancelar
        </button>
      </div>
    </form>
  );
}

export function ListForm({
  mode,
  election,
  rosterMembers,
  initial,
  onSaved,
  onCancel,
}: {
  mode: "submit" | "edit" | "commission";
  election: Election;
  rosterMembers: CommissionMember[];
  initial?: ElectionList | undefined;
  onSaved: (message: string) => Promise<void>;
  onCancel?: () => void;
}) {
  const id = useId();
  const { refresh } = useSession();
  const [name, setName] = useState(initial?.name ?? "");
  const [representativeId, setRepresentativeId] = useState("");
  const [picks, setPicks] = useState<Record<string, string>>(() => {
    const map: Record<string, string> = {};
    for (const pos of election.positions) {
      map[pos] = initial?.candidates.find((c) => c.position === pos)?.rosterMemberId ?? "";
    }
    return map;
  });
  const [nameError, setNameError] = useState<string | null>(null);
  const [repError, setRepError] = useState<string | null>(null);
  const [posErrors, setPosErrors] = useState<Record<string, string>>({});
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);

  const repRef = useRef<HTMLSelectElement>(null);
  const nameRef = useRef<HTMLInputElement>(null);
  const selectRefs = useRef<Record<string, HTMLSelectElement | null>>({});
  const statusRef = useRef<HTMLDivElement>(null);
  const focusNext = useRef<HTMLElement | null>(null);

  useEffect(() => {
    setName(initial?.name ?? "");
    const map: Record<string, string> = {};
    for (const pos of election.positions) {
      map[pos] = initial?.candidates.find((c) => c.position === pos)?.rosterMemberId ?? "";
    }
    setPicks(map);
    setNameError(null);
    setRepError(null);
    setPosErrors({});
    setStatus(null);
  }, [initial, election.positions]);

  useEffect(() => {
    if (!focusNext.current) return;
    focusNext.current.focus();
    focusNext.current = null;
  });

  const commissionIds = useMemo(() => new Set(election.commission.map((m) => m.rosterMemberId)), [election.commission]);
  const eligibleCandidates = useMemo(
    () => rosterMembers.filter((m) => !commissionIds.has(m.rosterMemberId)),
    [rosterMembers, commissionIds],
  );

  const heading = mode === "edit" ? "Editá la lista" : mode === "commission" ? "Cargá una lista" : "Presentá tu lista";
  const submitLabel = mode === "edit" ? "Guardar cambios" : mode === "commission" ? "Cargar lista" : "Presentar lista";

  async function submit() {
    setStatus(null);
    let firstInvalid: HTMLElement | null = null;
    let nextRepError: string | null = null;
    let nextNameError: string | null = null;
    const nextPosErrors: Record<string, string> = {};

    if (mode === "commission" && !representativeId) {
      nextRepError = errorMessage("invalid_representative", "election-list");
      firstInvalid ??= repRef.current;
    }
    const trimmedName = name.trim();
    if (!trimmedName || trimmedName.length > 80) {
      nextNameError = errorMessage("invalid_name", "election-list");
      firstInvalid ??= nameRef.current;
    }
    const seen = new Set<string>();
    for (const pos of election.positions) {
      const cand = picks[pos] ?? "";
      if (!cand) {
        nextPosErrors[pos] = "Elegí un candidato para este cargo.";
        firstInvalid ??= selectRefs.current[pos] ?? null;
      } else if (seen.has(cand)) {
        nextPosErrors[pos] = "Este alumno ya está elegido en otro cargo.";
        firstInvalid ??= selectRefs.current[pos] ?? null;
      } else {
        seen.add(cand);
      }
    }

    if (firstInvalid) {
      setRepError(nextRepError);
      setNameError(nextNameError);
      setPosErrors(nextPosErrors);
      focusNext.current = firstInvalid;
      firstInvalid.focus();
      return;
    }

    setRepError(null);
    setNameError(null);
    setPosErrors({});
    setBusy(true);
    const candidates = election.positions.map((pos) => ({ position: pos, rosterMemberId: picks[pos]! }));
    const result =
      mode === "edit" && initial
        ? await call(
            apiClient.PUT("/elections/{electionId}/lists/{listId}", {
              params: { path: { electionId: election.id, listId: initial.id } },
              body: { name: trimmedName, candidates },
            }),
          )
        : mode === "commission"
          ? await call(
              apiClient.POST("/elections/{electionId}/commission/lists", {
                params: { path: { electionId: election.id } },
                body: { name: trimmedName, representativeRosterMemberId: representativeId, candidates },
              }),
            )
          : await call(
              apiClient.POST("/elections/{electionId}/lists", {
                params: { path: { electionId: election.id } },
                body: { name: trimmedName, candidates },
              }),
            );
    setBusy(false);

    if (result.ok) {
      if (mode === "submit" || mode === "commission") {
        setName("");
        setRepresentativeId("");
        const empty: Record<string, string> = {};
        for (const pos of election.positions) empty[pos] = "";
        setPicks(empty);
      }
      const okMsg =
        mode === "edit" ? "Se guardaron los cambios de la lista." : mode === "commission" ? "Se cargó la lista." : "Se presentó tu lista.";
      await onSaved(okMsg);
      return;
    }

    if (result.code === "mfa_required" || result.code === "unauthenticated") void refresh();
    const msg = errorMessage(result.code, "election-list");
    if (result.code === "invalid_name" || result.code === "list_name_taken") {
      setNameError(msg);
      focusNext.current = nameRef.current;
      return;
    }
    if (result.code === "invalid_representative") {
      setRepError(msg);
      focusNext.current = repRef.current;
      return;
    }
    setStatus({ tone: "error", text: msg });
    focusNext.current = statusRef.current;
  }

  return (
    <section className="panel" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`}>{heading}</h2>
      <form
        noValidate
        aria-labelledby={`${id}-title`}
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <FormStatus status={status} ref={statusRef} />
        {mode === "commission" && (
          <div className="field">
            <label htmlFor={`${id}-rep`}>Apoderado</label>
            <select
              id={`${id}-rep`}
              ref={repRef}
              name="representativeRosterMemberId"
              required
              value={representativeId}
              aria-invalid={repError ? true : undefined}
              aria-describedby={repError ? `${id}-rep-error` : undefined}
              onChange={(event) => {
                setRepresentativeId(event.target.value);
                setRepError(null);
              }}
            >
              <option value="">Elegí un alumno</option>
              {rosterMembers.map((m) => (
                <option key={m.rosterMemberId} value={m.rosterMemberId}>
                  {m.fullName}
                </option>
              ))}
            </select>
            {repError && <FieldError id={`${id}-rep-error`}>{repError}</FieldError>}
          </div>
        )}
        <div className="field">
          <label htmlFor={`${id}-name`}>Nombre de la lista</label>
          <input
            id={`${id}-name`}
            ref={nameRef}
            name="name"
            type="text"
            maxLength={80}
            required
            autoComplete="off"
            value={name}
            aria-invalid={nameError ? true : undefined}
            aria-describedby={nameError ? `${id}-name-error` : undefined}
            onChange={(event) => {
              setName(event.target.value);
              setNameError(null);
            }}
          />
          {nameError && <FieldError id={`${id}-name-error`}>{nameError}</FieldError>}
        </div>
        <fieldset className="choices">
          <legend>Elegí un candidato para cada cargo</legend>
          {election.positions.map((pos, index) => {
            const fieldId = `${id}-pos-${index}`;
            const err = posErrors[pos];
            return (
              <div className="field" key={pos}>
                <label htmlFor={fieldId}>{pos}</label>
                <select
                  id={fieldId}
                  ref={(el) => {
                    selectRefs.current[pos] = el;
                  }}
                  name={pos}
                  required
                  value={picks[pos] ?? ""}
                  aria-invalid={err ? true : undefined}
                  aria-describedby={err ? `${fieldId}-error` : undefined}
                  onChange={(event) => {
                    const value = event.target.value;
                    setPicks((current) => ({ ...current, [pos]: value }));
                    setPosErrors((current) => {
                      if (!(pos in current)) return current;
                      const next = { ...current };
                      delete next[pos];
                      return next;
                    });
                  }}
                >
                  <option value="">Elegí un candidato</option>
                  {eligibleCandidates.map((m) => (
                    <option key={m.rosterMemberId} value={m.rosterMemberId}>
                      {m.fullName}
                    </option>
                  ))}
                </select>
                {err && <FieldError id={`${fieldId}-error`}>{err}</FieldError>}
              </div>
            );
          })}
        </fieldset>
        <div className="list-card__actions">
          <button type="submit" className="button button--primary" disabled={busy} aria-busy={busy || undefined}>
            {submitLabel}
          </button>
          {mode === "edit" && onCancel && (
            <button type="button" className="button button--secondary" disabled={busy} onClick={onCancel}>
              Cancelar
            </button>
          )}
        </div>
      </form>
    </section>
  );
}
