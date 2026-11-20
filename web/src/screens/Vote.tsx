import { useCallback, useEffect, useId, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { useSession } from "../auth/session.tsx";
import { FieldError, FormStatus, type Status } from "../components/Form.tsx";
import type { Election, ElectionList } from "./electionShared.ts";

type Turnout = components["schemas"]["Turnout"];

/** What the school is promised about the secret ballot (S13); the school doc quotes it verbatim. */
export const SECRET_BALLOT_GUARANTEE =
  "El voto es secreto para las autoridades de la escuela y para quienes administran la plataforma en la escuela. No es secreto para un operador con acceso root al servidor mientras la elección está abierta. No usamos esquemas criptográficos como firmas ciegas o mixnets.";

export function SecretBallotGuarantee() {
  return (
    <section className="panel" aria-labelledby="secret-ballot-title">
      <h2 id="secret-ballot-title">Cómo es el voto secreto</h2>
      <p className="small">{SECRET_BALLOT_GUARANTEE}</p>
    </section>
  );
}

const BLANK = "blank";

/**
 * Voting while the election is open, built for a low-end phone: plain radios, then a
 * confirmation step. Retrying after a dropped signal is safe: a vote already stored
 * answers `already_voted`, which reads as "Ya votaste".
 */
export function VotePanel({ election, lists, canVote = true }: { election: Election; lists: ElectionList[]; canVote?: boolean }) {
  const id = useId();
  const { refresh } = useSession();
  const [turnout, setTurnout] = useState<Turnout | null>(null);
  const [choice, setChoice] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<Status>(null);
  const [turnoutFailed, setTurnoutFailed] = useState(false);
  const [choiceError, setChoiceError] = useState<string | null>(null);
  // Where focus goes after the step that held it disappears. A new object on every request, so
  // focus moves after the render that shows what it points at (Form.tsx does the same).
  const [focusTarget, setFocus] = useState<{ to: "confirm" | "done" | "status" | "choice" } | null>(null);
  const setFocusTarget = (to: "confirm" | "done" | "status" | "choice" | null) => setFocus(to ? { to } : null);
  const statusRef = useRef<HTMLDivElement>(null);
  const confirmRef = useRef<HTMLHeadingElement>(null);
  const doneRef = useRef<HTMLParagraphElement>(null);
  const choiceRef = useRef<HTMLInputElement>(null);

  const failed = useCallback(
    (code: string) => {
      if (code === "mfa_required" || code === "unauthenticated") void refresh();
      setStatus({ tone: "error", text: errorMessage(code) });
    },
    [refresh],
  );

  const loadTurnout = useCallback(async () => {
    const result = await call(
      apiClient.GET("/elections/{electionId}/turnout", { params: { path: { electionId: election.id } } }),
    );
    setTurnoutFailed(!result.ok);
    if (result.ok) setTurnout(result.data);
    else failed(result.code);
    return result.ok ? result.data : null;
  }, [election.id, failed]);

  async function retry() {
    setStatus(null);
    setBusy(true);
    const loaded = await loadTurnout();
    setBusy(false);
    // The button that held focus is gone.
    if (loaded) setFocusTarget(loaded.hasVoted ? "done" : "choice");
  }

  useEffect(() => {
    void loadTurnout();
  }, [loadTurnout]);

  useEffect(() => {
    if (!focusTarget) return;
    const refs = { confirm: confirmRef, done: doneRef, status: statusRef, choice: choiceRef };
    refs[focusTarget.to].current?.focus();
  }, [focusTarget, turnout, confirming, choiceError]);

  async function vote() {
    if (busy || !choice) return;
    setStatus(null);
    setFocusTarget(null);
    setBusy(true);
    const result = await call(
      apiClient.POST("/elections/{electionId}/vote", { params: { path: { electionId: election.id } }, body: { choice } }),
    );
    setBusy(false);
    if (result.ok || result.code === "already_voted") {
      setConfirming(false);
      setTurnout((t) => (t ? { ...t, hasVoted: true } : t));
      setFocusTarget("done");
      await loadTurnout();
      return;
    }
    failed(result.code);
    setFocusTarget("status");
  }

  const chosenName = choice === BLANK ? "En blanco" : lists.find((l) => l.id === choice)?.name;

  return (
    <section className="panel" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`}>Votación</h2>
      {turnout && (
        <p>
          {turnout.voted} de {turnout.electorate} votaron
        </p>
      )}
      <FormStatus status={status} ref={statusRef} />
      {!turnout && turnoutFailed && (
        <div className="panel__actions">
          <button type="button" className="button button--secondary" disabled={busy} aria-busy={busy || undefined} onClick={() => void retry()}>
            Reintentar
          </button>
        </div>
      )}
      {canVote && turnout?.hasVoted && (
        <p ref={doneRef} tabIndex={-1}>
          Ya votaste
        </p>
      )}
      {canVote && turnout && !turnout.hasVoted && !confirming && (
        <form
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            if (!choice) {
              setChoiceError("Elegí una lista o votá en blanco para seguir.");
              setFocusTarget("choice");
              return;
            }
            setConfirming(true);
            setFocusTarget("confirm");
          }}
        >
          <fieldset className="choices vote-choices">
            <legend>Elegí una lista o votá en blanco</legend>
            {[...lists.map((l) => ({ value: l.id, label: l.name })), { value: BLANK, label: "En blanco" }].map((option, i) => (
              <div className="check" key={option.value}>
                <input
                  id={`${id}-${option.value}`}
                  ref={(choice ? option.value === choice : i === 0) ? choiceRef : undefined}
                  type="radio"
                  name={`${id}-choice`}
                  value={option.value}
                  checked={choice === option.value}
                  aria-invalid={choiceError ? true : undefined}
                  aria-describedby={choiceError ? `${id}-choice-error` : undefined}
                  onChange={() => {
                    setChoice(option.value);
                    setChoiceError(null);
                  }}
                />
                <label htmlFor={`${id}-${option.value}`}>
                  <bdi>{option.label}</bdi>
                </label>
              </div>
            ))}
            {choiceError && <FieldError id={`${id}-choice-error`}>{choiceError}</FieldError>}
          </fieldset>
          <div className="panel__actions">
            <button type="submit" className="button button--primary">
              Seguir
            </button>
          </div>
        </form>
      )}
      {canVote && turnout && !turnout.hasVoted && confirming && (
        <section>
          <h3 ref={confirmRef} tabIndex={-1}>
            Confirmá tu voto
          </h3>
          <p>
            Vas a votar: <strong><bdi>{chosenName}</bdi></strong>
          </p>
          <p className="muted small">Después de confirmar no se puede cambiar.</p>
          <div className="panel__actions">
            <button type="button" className="button button--primary" disabled={busy} aria-busy={busy || undefined} onClick={() => void vote()}>
              Confirmar
            </button>
            <button
              type="button"
              className="button button--secondary"
              disabled={busy}
              onClick={() => {
                setStatus(null);
                setConfirming(false);
                setFocusTarget("choice");
              }}
            >
              Volver
            </button>
          </div>
        </section>
      )}
    </section>
  );
}
