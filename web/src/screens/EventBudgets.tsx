import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { canReadContacts, hasRole, useMe } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";

type Budget = components["schemas"]["EventBudget"];
type Detail = components["schemas"]["EventBudgetDetail"];
type Line = components["schemas"]["EventBudgetLine"];
type LineInput = components["schemas"]["EventBudgetLineInput"];
type Heading = components["schemas"]["ContactHeading"];
type Contact = components["schemas"]["Contact"];
const pesos = (cents: number) => `$ ${new Intl.NumberFormat("es-AR", { maximumFractionDigits: 2 }).format(cents / 100)}`;
function formatExpenseDiff(cents: number): string {
  if (cents < 0) {
    const over = pesos(Math.abs(cents));
    return `\u2212${over} (te pasaste ${over})`;
  }
  return pesos(cents);
}
function formatIncomeDiff(cents: number): string {
  if (cents < 0) {
    const short = pesos(Math.abs(cents));
    return `\u2212${short} (faltan ${short})`;
  }
  return pesos(cents);
}
function parseCents(raw: string): number | null {
  const value = raw.trim().replace(",", ".");
  if (!/^\d+(\.\d{1,2})?$/.test(value)) return null;
  const [whole, frac = ""] = value.split(".");
  const cents = Number(whole) * 100 + Number(frac.padEnd(2, "0"));
  return Number.isSafeInteger(cents) && cents > 0 ? cents : null;
}

// The line form already checks the amount is a positive number, so the API's invalid_amount
// for a line means the budget totals would no longer fit.
const lineErrorMessage = (code: string) => code === "invalid_amount" ? "El total del presupuesto sería demasiado grande." : errorMessage(code);

function BudgetForm({ initial, save }: { initial?: Budget; save: (name: string, start: string, end: string) => Promise<string | null> }) {
  const form = useFormErrors();
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!form.validate({ name: (v) => v.trim() ? null : "Escrib\u00ed un nombre.", startsOn: (v) => v ? null : "Eleg\u00ed la fecha de inicio.", endsOn: (v) => v ? null : "Eleg\u00ed la fecha de fin." })) return;
    const startsOn = form.value("startsOn"), endsOn = form.value("endsOn");
    setBusy(true); const code = await save(form.value("name"), startsOn, endsOn); setBusy(false);
    if (code) {
      // The API answers invalid_range for both causes; ISO dates compare as text.
      const text = code !== "invalid_range" ? errorMessage(code) : endsOn < startsOn ? "La fecha de fin no puede ser anterior al inicio." : "El evento puede durar hasta 31 días.";
      setStatus({ tone: "error", text }); form.failed(code);
    }
    else setStatus({ tone: "ok", text: "Presupuesto guardado." });
  }
  return <form {...form.formProps} onSubmit={(e) => void submit(e)}>
    <Field label="Nombre" {...form.field("name")} defaultValue={initial?.name} />
    <Field label="Inicio" type="date" {...form.field("startsOn")} defaultValue={initial?.startsOn} />
    <Field label="Fin" type="date" {...form.field("endsOn")} defaultValue={initial?.endsOn} />
    <FormStatus {...form.statusProps} status={status} />
    <button className="button button--primary" disabled={busy} type="submit">{initial ? "Guardar cambios" : "Crear presupuesto"}</button>
  </form>;
}

function LineForm({ headings, contacts, initial, save, cancel }: { headings: Heading[]; contacts: Contact[]; initial?: Line | undefined; save: (input: LineInput) => Promise<string | null>; cancel?: (() => void) | undefined }) {
  const form = useFormErrors();
  const [status, setStatus] = useState<Status>(null);
  const [kind, setKind] = useState(initial?.kind ?? "cost");
  const [heading, setHeading] = useState(initial?.headingId ?? headings.find((h) => !h.archived)?.id ?? "");
  const [contact, setContact] = useState(initial?.contactId ?? "");
  const [price, setPrice] = useState("");
  const [amount, setAmount] = useState(initial?.unitAmountCents ? String(initial.unitAmountCents / 100) : "");
  const [busy, setBusy] = useState(false);
  const selectedContact = contacts.find((c) => c.id === contact);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!form.validate({ description: (v) => v.trim() ? null : "Escrib\u00ed una descripci\u00f3n.", quantity: (v) => kind === "ticket" && !v ? null : /^\d+$/.test(v) && (kind === "ticket" || Number(v) > 0) && Number.isSafeInteger(Number(v)) ? null : "Ingres\u00e1 una cantidad v\u00e1lida.", amount: (v) => kind === "ticket" && !v ? null : parseCents(v) ? null : "Ingres\u00e1 un importe positivo." })) return;
    if (kind === "cost" && !heading) { setStatus({ tone: "error", text: "Eleg\u00ed un rubro." }); form.failed("invalid_heading"); return; }
    const amountCents = amount ? parseCents(amount) : null;
    const input: LineInput = { kind, description: form.value("description"), ...(kind === "cost" ? { headingId: heading, ...(contact ? { contactId: contact } : {}) } : {}), ...(form.value("quantity") ? { quantity: Number(form.value("quantity")) } : {}), ...(amountCents ? { unitAmountCents: amountCents } : {}), ...(form.value("day") ? { day: form.value("day") } : {}) };
    setBusy(true); const code = await save(input); setBusy(false);
    if (code) { setStatus({ tone: "error", text: lineErrorMessage(code) }); form.failed(code); } else { setStatus({ tone: "ok", text: "L\u00ednea guardada." }); if (cancel) cancel(); }
  }
  return <form {...form.formProps} onSubmit={(e) => void submit(e)}>
    <div className="field"><label htmlFor="budget-kind">Tipo</label><select id="budget-kind" value={kind} onChange={(e) => setKind(e.target.value)}><option value="cost">Costo</option><option value="ticket">Entradas</option><option value="foodtruck">Canon de puestos</option><option value="sponsor">Sponsor</option><option value="due">Cuotas</option></select></div>
    {kind === "cost" && <><div className="field"><label htmlFor="budget-heading">Rubro</label><select id="budget-heading" value={heading} onChange={(e) => setHeading(e.target.value)}><option value="">{"Eleg\u00ed un rubro"}</option>{headings.map((h) => <option key={h.id} value={h.id}>{h.name}</option>)}</select></div>
    <div className="field"><label htmlFor="budget-contact">Contacto</label><select id="budget-contact" value={contact} onChange={(e) => { setContact(e.target.value); setPrice(""); }}><option value="">Sin contacto</option>{contacts.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
    {selectedContact && <div className="field"><label htmlFor="budget-price">Precio de referencia</label><select id="budget-price" value={price} onChange={(e) => { setPrice(e.target.value); const p = selectedContact.referencePrices.find((v) => v.id === e.target.value); if (p) setAmount(String(p.amountCents / 100)); }}><option value="">Sin precio de referencia</option>{selectedContact.referencePrices.map((p) => <option key={p.id} value={p.id}>{p.description} - {pesos(p.amountCents)}</option>)}</select></div>}</>}
    <Field label={"Descripci\u00f3n"} {...form.field("description")} defaultValue={initial?.description} />
    <Field key={kind} label={kind === "ticket" ? "Asistencia esperada" : "Cantidad"} inputMode="numeric" {...form.field("quantity")} readOnly={kind === "due"} defaultValue={kind === "due" ? "1" : initial?.quantity ?? (kind === "cost" || kind === "ticket" ? "" : "1")} />
    <Field label="Precio unitario ($)" inputMode="decimal" {...form.field("amount")} value={amount} onChange={(e) => setAmount(e.target.value)} />
    <Field label={"D\u00eda (opcional)"} type="date" {...form.field("day")} defaultValue={initial?.day} />
    <FormStatus {...form.statusProps} status={status} />
    <button type="submit" disabled={busy} className="button button--primary">{initial ? "Guardar l\u00ednea" : "Agregar l\u00ednea"}</button>{cancel && <button type="button" className="button button--secondary" onClick={cancel}>Cancelar</button>}
  </form>;
}

export function EventBudgets() {
  useDocumentTitle("Presupuestos de evento");
  const me = useMe(); const canRead = canReadContacts(me); const canWrite = hasRole(me, "officer");
  const [budgets, setBudgets] = useState<Budget[]>([]); const [detail, setDetail] = useState<Detail | null>(null);
  const [headings, setHeadings] = useState<Heading[]>([]); const [contacts, setContacts] = useState<Contact[]>([]);
  const [status, setStatus] = useState<Status>(null); const [editing, setEditing] = useState(false); const [lineEdit, setLineEdit] = useState<Line | null>(null); const [confirm, setConfirm] = useState<"delete" | "archive" | string | null>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const focusStatus = useRef(false);
  const detailReq = useRef(0);
  useEffect(() => {
    if (!focusStatus.current || !status) return;
    focusStatus.current = false;
    statusRef.current?.focus();
  }, [status]);
  const loadList = useCallback(async () => { const result = await call(apiClient.GET("/event-budgets")); if (result.ok) setBudgets(result.data); else setStatus({ tone: "error", text: errorMessage(result.code) }); }, []);
  const loadDetail = useCallback(async (id: string) => { const seq = ++detailReq.current; const result = await call(apiClient.GET("/event-budgets/{id}", { params: { path: { id } } })); if (seq !== detailReq.current) return; if (result.ok) setDetail(result.data); else setStatus({ tone: "error", text: errorMessage(result.code) }); }, []);
  useEffect(() => { if (!canRead) return; void loadList(); if (canWrite) { void call(apiClient.GET("/contacts/headings")).then((r) => { if (r.ok) setHeadings(r.data); else setStatus({ tone: "error", text: errorMessage(r.code) }); }); void call(apiClient.GET("/contacts")).then((r) => { if (r.ok) setContacts(r.data); else setStatus({ tone: "error", text: errorMessage(r.code) }); }); } }, [canRead, canWrite, loadList]);
  if (!canRead) return <><PageTitle>Presupuestos de evento</PageTitle><p role="alert">{"No ten\u00e9s acceso a esta secci\u00f3n."}</p></>;
  async function saveBudget(name: string, startsOn: string, endsOn: string): Promise<string | null> {
    const result = detail && editing ? await call(apiClient.PUT("/event-budgets/{id}", { params: { path: { id: detail.id } }, body: { name, startsOn, endsOn } })) : await call(apiClient.POST("/event-budgets", { body: { name, startsOn, endsOn } }));
    if (!result.ok) return result.code; await loadList(); await loadDetail(result.data.id); setEditing(false); return null;
  }
  async function saveLine(input: LineInput): Promise<string | null> {
    if (!detail) return "not_found";
    const result = lineEdit ? await call(apiClient.PUT("/event-budgets/{id}/lines/{lineId}", { params: { path: { id: detail.id, lineId: lineEdit.id } }, body: input })) : await call(apiClient.POST("/event-budgets/{id}/lines", { params: { path: { id: detail.id } }, body: input }));
    if (!result.ok) return result.code; await loadDetail(detail.id); return null;
  }
  async function confirmAction() {
    if (!detail || !confirm) return; let result;
    if (confirm === "delete") result = await call(apiClient.DELETE("/event-budgets/{id}", { params: { path: { id: detail.id } } }));
    else if (confirm === "archive") result = await call(apiClient.POST("/event-budgets/{id}/archive", { params: { path: { id: detail.id } } }));
    else result = await call(apiClient.DELETE("/event-budgets/{id}/lines/{lineId}", { params: { path: { id: detail.id, lineId: confirm } } }));
    setConfirm(null); if (!result.ok) { focusStatus.current = true; setStatus({ tone: "error", text: errorMessage(result.code) }); return; }
    if (confirm === "delete") setDetail(null); else await loadDetail(detail.id); await loadList();
  }
  const byHeading = detail?.byHeading ?? [];
  const expenseRows = byHeading.filter((r) => r.budgetedExpenseCents !== 0 || r.actualExpenseCents !== 0);
  const incomeRows = byHeading.filter((r) => r.budgetedIncomeCents !== 0 || r.actualIncomeCents !== 0);
  return <><header className="page-header"><div><PageTitle>Presupuestos de evento</PageTitle><p>{"Organiz\u00e1 los costos e ingresos previstos de cada evento."}</p></div></header>
    <Link to="/calculadora">Calculadora</Link><FormStatus ref={statusRef} status={status} />
    <section aria-label="Presupuestos"><h2>Presupuestos</h2>{canWrite && detail && <button type="button" className="button button--secondary" onClick={() => { setDetail(null); setEditing(false); setLineEdit(null); }}>Nuevo presupuesto</button>}{budgets.length === 0 && <p>{"Todav\u00eda no hay presupuestos."}</p>}<ul>{budgets.map((b) => <li key={b.id}><button type="button" className="text-button" onClick={() => { setEditing(false); setLineEdit(null); void loadDetail(b.id); }}>{b.name}</button>{b.archived && " (archivado)"}</li>)}</ul></section>
    {canWrite && !detail && <section><h2>Crear presupuesto</h2><BudgetForm save={saveBudget} /></section>}
    {detail && <section aria-label="Detalle del presupuesto"><h2>{detail.name}</h2><p>{detail.startsOn} - {detail.endsOn}{detail.archived && " (archivado)"}</p>
      {canWrite && !detail.archived && <><button className="button button--secondary" type="button" onClick={() => setEditing(!editing)}>Editar presupuesto</button><button className="button button--secondary" type="button" onClick={() => setConfirm("archive")}>Archivar</button><button className="button button--secondary" type="button" onClick={() => setConfirm("delete")}>Borrar</button>{editing && <BudgetForm key={detail.id} initial={detail} save={saveBudget} />}</>}
      <h3>Resultado total</h3><p>Costo: {pesos(detail.totals.costCents)}; ingresos: {pesos(detail.totals.incomeCents)}; resultado: {pesos(detail.totals.resultCents)}. Entrada para no perder: {detail.totals.breakEvenCents === null ? "sin calcular" : pesos(detail.totals.breakEvenCents)}.</p>
      <h3>Presupuestado vs real por rubro</h3>
      {expenseRows.length === 0 && incomeRows.length === 0 && <p>{"Todav\u00eda no hay rubros presupuestados ni movimientos ligados."}</p>}
      {expenseRows.length > 0 && <>
        <h4 id="budget-actual-expenses">Gastos</h4>
        <div className="table-wrap" tabIndex={0} role="region" aria-labelledby="budget-actual-expenses">
          <table className="data-table">
            <thead>
              <tr>
                <th scope="col">Rubro</th>
                <th scope="col">Presupuestado</th>
                <th scope="col">Real</th>
                <th scope="col">Diferencia</th>
              </tr>
            </thead>
            <tbody>
              {expenseRows.map((row) => (
                <tr key={row.headingId}>
                  <th scope="row">{row.headingName}</th>
                  <td data-label="Presupuestado">{pesos(row.budgetedExpenseCents)}</td>
                  <td data-label="Real">{pesos(row.actualExpenseCents)}</td>
                  <td data-label="Diferencia">{formatExpenseDiff(row.expenseDifferenceCents)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </>}
      {incomeRows.length > 0 && <>
        <h4 id="budget-actual-income">Ingresos</h4>
        <div className="table-wrap" tabIndex={0} role="region" aria-labelledby="budget-actual-income">
          <table className="data-table">
            <thead>
              <tr>
                <th scope="col">Rubro</th>
                <th scope="col">Presupuestado</th>
                <th scope="col">Real</th>
                <th scope="col">Diferencia</th>
              </tr>
            </thead>
            <tbody>
              {incomeRows.map((row) => (
                <tr key={row.headingId}>
                  <th scope="row">{row.headingName}</th>
                  <td data-label="Presupuestado">{pesos(row.budgetedIncomeCents)}</td>
                  <td data-label="Real">{pesos(row.actualIncomeCents)}</td>
                  <td data-label="Diferencia">{formatIncomeDiff(row.incomeDifferenceCents)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </>}
      <h3>{"Por d\u00eda"}</h3><ul>{detail.days.map((day) => <li key={day.day}>{day.day}: costo {pesos(day.totals.costCents)}, ingresos {pesos(day.totals.incomeCents)}, resultado {pesos(day.totals.resultCents)}, entrada {day.totals.breakEvenCents === null ? "sin calcular" : pesos(day.totals.breakEvenCents)}</li>)}</ul>
      <h3>{"L\u00edneas"}</h3><ul>{detail.lines.map((line) => <li key={line.id}>{line.description} - {line.quantity ?? "-"} x {line.unitAmountCents == null ? "sin precio" : pesos(line.unitAmountCents)} {canWrite && !detail.archived && <><button type="button" className="text-button" onClick={() => setLineEdit(line)}>Editar</button><button type="button" className="text-button" onClick={() => setConfirm(line.id)}>Borrar</button></>}</li>)}</ul>
      {canWrite && !detail.archived && <><h3>{lineEdit ? "Editar l\u00ednea" : "Agregar l\u00ednea"}</h3><LineForm key={lineEdit?.id ?? "new"} headings={headings} contacts={contacts} initial={lineEdit ?? undefined} save={saveLine} cancel={lineEdit ? () => setLineEdit(null) : undefined} /></>}
    </section>}
    {confirm && <ConfirmDialog title={confirm === "archive" ? "Archivar presupuesto" : "Confirmar borrado"} confirmLabel={confirm === "archive" ? "Archivar" : "Borrar"} onCancel={() => setConfirm(null)} onConfirm={() => void confirmAction()}><p>{"Esta acci\u00f3n afecta el presupuesto seleccionado."}</p></ConfirmDialog>}
  </>;
}
