import { useCallback, useEffect, useId, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { useSession } from "../auth/session.tsx";
import { FormStatus, type Status } from "../components/Form.tsx";

type ElectionResult = components["schemas"]["ElectionResult"];

function outcomeText(result: ElectionResult): string {
  switch (result.outcome) {
    case "winner":
      return `Ganó ${result.winner?.name ?? ""}`;
    case "tie":
      return "Sin ganador: empate";
    case "no_lists":
      return "Sin ganador: no hubo listas aprobadas";
    default:
      return "Sin ganador";
  }
}

/** The result of a closed election: votes per list, blank votes, turnout and the winner or "Sin ganador". */
export function ResultsPanel({ electionId }: { electionId: string }) {
  const id = useId();
  const { refresh } = useSession();
  const [result, setResult] = useState<ElectionResult | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const outcomeRef = useRef<HTMLParagraphElement>(null);
  const focusOutcome = useRef(false);

  const load = useCallback(async () => {
    const res = await call(apiClient.GET("/elections/{electionId}/results", { params: { path: { electionId } } }));
    if (res.ok) {
      setStatus(null);
      setResult(res.data);
    } else {
      if (res.code === "mfa_required" || res.code === "unauthenticated") void refresh();
      setStatus({ tone: "error", text: errorMessage(res.code) });
    }
    return res.ok;
  }, [electionId, refresh]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!focusOutcome.current || !result) return;
    focusOutcome.current = false;
    outcomeRef.current?.focus();
  }, [result]);

  async function retry() {
    setBusy(true);
    // The button that holds focus goes away once the result shows.
    focusOutcome.current = true;
    if (!(await load())) focusOutcome.current = false;
    setBusy(false);
  }

  // An annulled result carries no counts; the election details already show the reason.
  const published = result?.status === "published" ? result : null;

  return (
    <section className="panel" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`}>Resultado</h2>
      <FormStatus status={status} />
      {!result && status && (
        <div className="panel__actions">
          <button type="button" className="button button--secondary" disabled={busy} aria-busy={busy || undefined} onClick={() => void retry()}>
            Reintentar
          </button>
        </div>
      )}
      {published && (
        <>
          <p ref={outcomeRef} tabIndex={-1}>
            <strong>{outcomeText(published)}</strong>
          </p>
          <p>
            Participación: {published.voted ?? 0} de {published.electorate ?? 0}
          </p>
          {(published.lists?.length ?? 0) > 0 && (
            <div className="table-wrap" tabIndex={0} role="region" aria-labelledby={`${id}-caption`}>
              <table className="data-table">
                <caption id={`${id}-caption`} className="visually-hidden">
                  Votos por lista
                </caption>
                <thead>
                  <tr>
                    <th scope="col">Lista</th>
                    <th scope="col">Votos</th>
                  </tr>
                </thead>
                <tbody>
                  {published.lists?.map((l) => (
                    <tr key={l.id}>
                      <th scope="row">
                        <bdi>{l.name}</bdi>
                      </th>
                      <td data-label="Votos">{l.votes}</td>
                    </tr>
                  ))}
                  <tr>
                    <th scope="row">Voto en blanco</th>
                    <td data-label="Votos">{published.blankVotes ?? 0}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </section>
  );
}
