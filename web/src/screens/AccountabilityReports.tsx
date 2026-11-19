import { useEffect, useId, useRef, useState } from "react";
import { errorMessage } from "../api/errors.ts";
import { moneyRequest } from "../api/money.ts";
import type { components } from "../api/schema.gen.ts";
import { hasRole, useMe, useSession } from "../auth/session.tsx";
import { Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";
import { dayMonthYear, pesos, todayArgentina } from "../lib/money.ts";

type Summary = components["schemas"]["AccountabilityReportSummary"];
type Total = Omit<components["schemas"]["FinanceCategoryTotal"], "totalCents"> & { totalCents: string };
type Entry = Omit<components["schemas"]["AccountabilityEntry"], "amountCents"> & { amountCents: string };
type DueTotal = Omit<components["schemas"]["AccountabilityDueTotal"], "amountCents"> & { amountCents: string };
// moneyRequest keeps every *Cents value as an exact decimal string (int64 beyond 2^53).
type Report = Summary & { openingCents: string; closingCents: string; totals: Total[]; dues: DueTotal[]; entries: Entry[] };
type Preview = { from: string; to: string; term: string; openingCents: string; closingCents: string; incomeCents: string; expenseCents: string; entries: unknown[] };

const NOTICE = "Una vez publicada no se puede cambiar.";
const period = (r: { from: string; to: string }) => `Del ${dayMonthYear(r.from)} al ${dayMonthYear(r.to)}`;
const csvHref = (id: string) => `/api/finance/accountability-reports/${encodeURIComponent(id)}.csv`;

/** "Caja → Rendiciones": the published Financial accountability reports; Officers publish new ones. */
export function AccountabilityReports() {
  useDocumentTitle("Rendiciones de cuentas");
  const me = useMe();
  const { refresh } = useSession();
  const isOfficer = hasRole(me, "officer");
  const [list, setList] = useState<Summary[] | null | undefined>(undefined);
  const [listStatus, setListStatus] = useState<Status>(null);
  const [open, setOpen] = useState<Report | null>(null);
  const ids = useId();

  function authError(code: string) {
    if (code === "mfa_required" || code === "unauthenticated") void refresh();
  }

  async function load() {
    const result = await moneyRequest<Summary[]>("/finance/accountability-reports");
    if (result.ok) { setList(result.data); return; }
    authError(result.code);
    setList(null);
    setListStatus({ tone: "error", text: errorMessage(result.code, "finance") });
  }
  useEffect(() => { void load(); }, []);

  async function show(id: string) {
    const result = await moneyRequest<Report>(`/finance/accountability-reports/${encodeURIComponent(id)}`);
    if (result.ok) { setOpen(result.data); setListStatus(null); return; }
    authError(result.code);
    setListStatus({ tone: "error", text: errorMessage(result.code, "finance") });
  }

  return <>
    <header className="page-header"><PageTitle>Rendiciones de cuentas</PageTitle></header>
    <p><Link to="/caja">Volver a Caja</Link></p>
    {isOfficer && <PublishPanel onPublished={(report) => { setOpen(report); void load(); }} authError={authError} />}
    <section className="panel" aria-labelledby={`${ids}-list`}>
      <h2 id={`${ids}-list`}>Rendiciones publicadas</h2>
      <FormStatus status={listStatus} />
      {list === undefined && <p>Cargando...</p>}
      {list && list.length === 0 && <p>Todavía no se publicó ninguna rendición.</p>}
      {list && list.length > 0 && <ul>{list.map((r) => <li key={r.id}>
        <strong>{r.term}</strong><br />
        <span>{period(r)}</span><br />
        <span>Publicada por {r.publishedBy}</span>
        <div className="panel__actions">
          <button type="button" className="button button--secondary" onClick={() => void show(r.id)}>Ver</button>
          <a className="button button--secondary" href={csvHref(r.id)} download>Descargar CSV</a>
        </div>
      </li>)}</ul>}
    </section>
    {open && <ReportView report={open} />}
  </>;
}

/** Officer-only form: choose the period and Term, preview it with the notice, then publish. */
function PublishPanel({ onPublished, authError }: { onPublished: (report: Report) => void; authError: (code: string) => void }) {
  const [term, setTerm] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState(todayArgentina);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const sending = useRef(false);
  const form = useFormErrors();
  const ids = useId();

  async function showPreview() {
    if (!form.validate({
      term: (v) => !v.trim() ? "Escribí la gestión, por ejemplo «Gestión 2026»." : null,
      from: (v) => !v ? "Elegí la fecha de inicio." : null,
      to: (v) => !v ? "Elegí la fecha de fin." : from && v < from ? "La fecha «Hasta» no puede ser anterior a «Desde»." : null,
    })) return;
    setBusy(true); setStatus(null);
    const result = await moneyRequest<Omit<Preview, "term">>(`/finance/report?${new URLSearchParams({ from, to })}`);
    setBusy(false);
    if (result.ok) { setPreview({ ...result.data, term: term.trim() }); return; }
    authError(result.code);
    setStatus({ tone: "error", text: errorMessage(result.code, "finance") });
    form.failed(result.code);
  }

  async function publish() {
    if (!preview || sending.current) return;
    sending.current = true;
    setBusy(true); setStatus(null);
    const result = await moneyRequest<Report>("/finance/accountability-reports", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ from: preview.from, to: preview.to, term: preview.term }),
    });
    sending.current = false;
    setBusy(false);
    if (result.ok) {
      setPreview(null);
      setStatus({ tone: "ok", text: "Rendición publicada." });
      onPublished(result.data);
      return;
    }
    authError(result.code);
    setPreview(null);
    setStatus({ tone: "error", text: errorMessage(result.code, "finance") });
    form.failed(result.code);
  }

  // Any change to the form discards the preview: what is published is what was previewed.
  const edit = (set: (value: string) => void) => (event: { target: { value: string } }) => { set(event.target.value); setPreview(null); };

  return <section className="panel" aria-labelledby={`${ids}-publish`}>
    <h2 id={`${ids}-publish`}>Publicar una rendición</h2>
    <form {...form.formProps} onSubmit={(event) => { event.preventDefault(); void showPreview(); }}>
      <Field label="Gestión" hint="Por ejemplo «Gestión 2026»." {...form.field("term")} value={term} onChange={edit(setTerm)} />
      <Field label="Desde" type="date" {...form.field("from")} value={from} onChange={edit(setFrom)} />
      <Field label="Hasta" type="date" {...form.field("to")} value={to} onChange={edit(setTo)} />
      <div className="panel__actions">
        <button className="button button--secondary" disabled={busy}>Vista previa</button>
      </div>
    </form>
    <FormStatus status={status} {...form.statusProps} />
    {preview && <div aria-labelledby={`${ids}-preview`} role="region">
      <h3 id={`${ids}-preview`}>Vista previa</h3>
      <p><strong>{preview.term}</strong> {"—"} {period(preview)}</p>
      <dl className="report-summary">
        <div><dt>Saldo inicial</dt><dd>{pesos(preview.openingCents)}</dd></div>
        <div><dt>Total de ingresos</dt><dd>{pesos(preview.incomeCents)}</dd></div>
        <div><dt>Total de gastos</dt><dd>{pesos(preview.expenseCents)}</dd></div>
        <div><dt>Saldo final</dt><dd>{pesos(preview.closingCents)}</dd></div>
      </dl>
      <p>{preview.entries.length === 1 ? "1 movimiento" : `${preview.entries.length} movimientos`} en el período.</p>
      <p role="note"><strong>{NOTICE}</strong></p>
      <div className="panel__actions">
        <button type="button" className="button button--primary" disabled={busy} onClick={() => void publish()}>Publicar</button>
      </div>
    </div>}
  </section>;
}

/** One published report: balances, totals per Category and per Due, and its Entries as published. */
function ReportView({ report }: { report: Report }) {
  const ids = useId();
  const totals = (direction: "income" | "expense", title: string) => {
    const rows = report.totals.filter((t) => t.direction === direction);
    if (rows.length === 0) return null;
    return <>
      <h3 id={`${ids}-${direction}`}>{title}</h3>
      <ul className="report-list" aria-labelledby={`${ids}-${direction}`}>
        {rows.map((t) => <li key={t.categoryId}><span>{t.name}</span><span>{pesos(t.totalCents)}</span></li>)}
      </ul>
    </>;
  };
  return <section className="panel" aria-labelledby={`${ids}-title`}>
    <h2 id={`${ids}-title`}>{report.term}</h2>
    <p>{period(report)}. Publicada por {report.publishedBy}.</p>
    <dl className="report-summary">
      <div><dt>Saldo inicial</dt><dd>{pesos(report.openingCents)}</dd></div>
      <div><dt>Saldo final</dt><dd>{pesos(report.closingCents)}</dd></div>
    </dl>
    {totals("income", "Ingresos")}
    {totals("expense", "Gastos")}
    {report.dues.length > 0 && <>
      <h3 id={`${ids}-dues`}>Cuotas</h3>
      <ul className="report-list" aria-labelledby={`${ids}-dues`}>
        {report.dues.map((d) => <li key={d.dueId}><span>{d.name}</span><span>{d.paidCount} de {d.totalCount} pagaron</span></li>)}
      </ul>
    </>}
    <h3 id={`${ids}-entries`}>Movimientos</h3>
    {report.entries.length === 0 ? <p>No hubo movimientos en el período.</p> : <ul aria-labelledby={`${ids}-entries`}>{report.entries.map((e) => <li key={e.id}>
      <strong>{e.description}</strong> {"—"} {pesos(e.amountCents)} ({e.direction === "income" ? "Ingreso" : "Gasto"})<br />
      <span>{e.category} {"—"} {dayMonthYear(e.date)}</span>
    </li>)}</ul>}
    <p><a className="button button--secondary" href={csvHref(report.id)} download>Descargar CSV</a></p>
  </section>;
}
