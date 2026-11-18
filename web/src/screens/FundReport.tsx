import { useEffect, useId, useRef, useState } from "react";
import { errorMessage } from "../api/errors.ts";
import { moneyRequest } from "../api/money.ts";
import type { components } from "../api/schema.gen.ts";
import { useSession } from "../auth/session.tsx";
import { Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";
import { dayMonthYear, pesos, todayArgentina } from "../lib/money.ts";

type Entry = Omit<components["schemas"]["FinanceEntry"], "amountCents"> & { amountCents: string };
type Total = Omit<components["schemas"]["FinanceCategoryTotal"], "totalCents"> & { totalCents: string };
// moneyRequest keeps every *Cents value as an exact decimal string (int64 beyond 2^53).
type Report = Omit<components["schemas"]["FinanceReport"], "entries" | "totals" | "openingCents" | "closingCents" | "incomeCents" | "expenseCents">
  & { entries: Entry[]; totals: Total[]; openingCents: string; closingCents: string; incomeCents: string; expenseCents: string };

const query = (from: string, to: string) => `?${new URLSearchParams({ from, to })}`;

/** "Caja → Informe": the fund report for a date range, computed on request. */
export function FundReport() {
  useDocumentTitle("Informe de caja");
  const { refresh } = useSession();
  const [from, setFrom] = useState(() => `${todayArgentina().slice(0, 8)}01`);
  const [to, setTo] = useState(todayArgentina);
  const [report, setReport] = useState<Report | null | undefined>(undefined);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const latest = useRef(0);
  const form = useFormErrors();
  const ids = useId();

  async function load(start: string, end: string, submitted: boolean) {
    const request = ++latest.current;
    setBusy(true); setStatus(null);
    const result = await moneyRequest<Report>(`/finance/report${query(start, end)}`);
    if (request !== latest.current) return;
    setBusy(false);
    if (result.ok) { setReport(result.data); return; }
    if (result.code === "mfa_required" || result.code === "unauthenticated") void refresh();
    setReport(null);
    setStatus({ tone: "error", text: errorMessage(result.code, "finance") });
    if (submitted) form.failed(result.code);
  }
  // The default range loads once on arrival; later ranges load when asked.
  const initial = useRef({ from, to });
  useEffect(() => { void load(initial.current.from, initial.current.to, false); }, []);

  function submit() {
    if (!form.validate({
      from: (v) => !v ? "Elegí la fecha de inicio." : null,
      to: (v) => !v ? "Elegí la fecha de fin." : from && v < from ? "La fecha «Hasta» no puede ser anterior a «Desde»." : null,
    })) return;
    void load(from, to, true);
  }

  const valid = !!from && !!to && from <= to;
  const names = new Map(report?.totals.map((t) => [t.categoryId, t.name]));
  const totals = (direction: "income" | "expense", title: string, none: string) => {
    const rows = report?.totals.filter((t) => t.direction === direction) ?? [];
    return <>
      <h3 id={`${ids}-${direction}`}>{title}</h3>
      {rows.length === 0 ? <p>{none}</p> : <ul className="report-list" aria-labelledby={`${ids}-${direction}`}>
        {rows.map((t) => <li key={t.categoryId}><span>{t.name}</span><span>{pesos(t.totalCents)}</span></li>)}
      </ul>}
    </>;
  };

  return <>
    <header className="page-header"><PageTitle>Informe de caja</PageTitle></header>
    <p><Link to="/caja">Volver a Caja</Link></p>
    <section className="panel">
      <form {...form.formProps} onSubmit={(event) => { event.preventDefault(); submit(); }}>
        <Field label="Desde" type="date" {...form.field("from")} value={from} onChange={(event) => setFrom(event.target.value)} />
        <Field label="Hasta" type="date" {...form.field("to")} value={to} onChange={(event) => setTo(event.target.value)} />
        <div className="panel__actions">
          <button className="button button--primary" disabled={busy}>Ver informe</button>
          {valid && <a className="button button--secondary" href={`/api/finance/report.csv${query(from, to)}`} download>Descargar CSV</a>}
        </div>
      </form>
      <FormStatus status={status} {...form.statusProps} />
    </section>
    {report === undefined && <p>Cargando...</p>}
    {report && <>
      <section className="panel" aria-labelledby={`${ids}-summary`}>
        <h2 id={`${ids}-summary`}>Saldos</h2>
        <p>Del {dayMonthYear(report.from)} al {dayMonthYear(report.to)}</p>
        <dl className="report-summary">
          <div><dt>Saldo inicial</dt><dd>{pesos(report.openingCents)}</dd></div>
          <div><dt>Total de ingresos</dt><dd>{pesos(report.incomeCents)}</dd></div>
          <div><dt>Total de gastos</dt><dd>{pesos(report.expenseCents)}</dd></div>
          <div><dt>Saldo final</dt><dd>{pesos(report.closingCents)}</dd></div>
        </dl>
      </section>
      <section className="panel" aria-labelledby={`${ids}-totals`}>
        <h2 id={`${ids}-totals`}>{"Totales por categoría"}</h2>
        {totals("income", "Ingresos", "Sin ingresos en el rango.")}
        {totals("expense", "Gastos", "Sin gastos en el rango.")}
      </section>
      <section className="panel" aria-labelledby={`${ids}-entries`}>
        <h2 id={`${ids}-entries`}>Movimientos del rango</h2>
        {report.entries.length === 0 ? <p>No hay movimientos entre esas fechas.</p> : <ul>{report.entries.map((entry) => <li key={entry.id}>
          <strong>{entry.description}</strong> {"—"} {pesos(entry.amountCents)} ({entry.direction === "income" ? "Ingreso" : "Gasto"})<br />
          <span>{names.get(entry.categoryId)} {"—"} {dayMonthYear(entry.date)}</span><br />
          Registrado por {entry.recordedBy}
        </li>)}</ul>}
      </section>
    </>}
  </>;
}
