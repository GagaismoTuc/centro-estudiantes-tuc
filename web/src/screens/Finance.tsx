import { useCallback, useEffect, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import { createFinanceEntry, financeBalance, financeEntries } from "../api/finance.ts";
import type { components } from "../api/schema.gen.ts";
import { hasRole, useMe, useSession } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";

type Category = components["schemas"]["FinanceCategory"];
type Entry = Omit<components["schemas"]["FinanceEntry"], "amountCents"> & { amountCents: string };
const pesos = (value: string) => {
  const cents = BigInt(value); const negative = cents < 0n; const absolute = negative ? -cents : cents;
  return `$ ${negative ? "-" : ""}${new Intl.NumberFormat("es-AR").format(absolute / 100n)},${String(absolute % 100n).padStart(2, "0")}`;
};
const dayMonthYear = (isoDate: string) => isoDate.split("-").reverse().join("/");
const today = () => new Intl.DateTimeFormat("en-CA", { timeZone: "America/Argentina/Buenos_Aires", year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date());

function centsOf(value: string): string | null {
  const match = /^(\d+)(?:[,.](\d{1,2}))?$/.exec(value.trim());
  if (!match || match[1]!.length > 17) return null;
  const cents = BigInt(match[1]!) * 100n + BigInt((match[2] ?? "").padEnd(2, "0"));
  if (cents <= 0n || cents > 9223372036854775807n) return null;
  return String(cents);
}

export function Finance() {
  useDocumentTitle("Caja");
  const me = useMe(); const { refresh } = useSession();
  const [entries, setEntries] = useState<Entry[] | null>(null);
  const [balance, setBalance] = useState<string | null>(null);
  const [categories, setCategories] = useState<Category[]>([]);
  const [open, setOpen] = useState(false);
  const [direction, setDirection] = useState("income");
  const [category, setCategory] = useState("");
  const [date, setDate] = useState(today);
  const [amount, setAmount] = useState("");
  const [description, setDescription] = useState("");
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const pending = useRef(false); const categoryRef = useRef<HTMLSelectElement>(null);
  const [categoryError, setCategoryError] = useState("");
  const [confirmReverse, setConfirmReverse] = useState<Entry | null>(null);
  const form = useFormErrors();
  const failed = useCallback((code: string) => {
    if (code === "mfa_required" || code === "unauthenticated") void refresh();
    setStatus({ tone: "error", text: errorMessage(code, "finance") });
  }, [refresh]);
  const load = useCallback(async () => {
    const [list, total, cats] = await Promise.all([financeEntries(), financeBalance(), call(apiClient.GET("/finance/categories"))]);
    if (list.ok) setEntries(list.data); else failed(list.code);
    if (total.ok) setBalance(total.data.balanceCents); else failed(total.code);
    if (cats.ok) setCategories(cats.data); else failed(cats.code);
  }, [failed]);
  useEffect(() => { void load(); }, [load]);

  async function save() {
    if (pending.current) return;
    if (!category) { setCategoryError("Eleg\u00ed una categor\u00eda."); categoryRef.current?.focus(); return; }
    if (!form.validate({
      date: (v) => !v ? "Eleg\u00ed una fecha." : null,
      amount: (v) => centsOf(v) === null ? "Ingres\u00e1 un monto positivo con hasta dos decimales." : null,
      description: (v) => !v.trim() ? "Escrib\u00ed una descripci\u00f3n." : Array.from(v.trim()).length > 500 ? "La descripci\u00f3n puede tener hasta 500 caracteres." : null,
    })) return;
    pending.current = true; setBusy(true); setStatus(null);
    const result = await createFinanceEntry({ categoryId: category, direction, date, amountCents: centsOf(amount)!, description });
    if (result.ok) {
      setOpen(false); setAmount(""); setDescription(""); setCategory("");
      setStatus({ tone: "ok", text: "Se registr\u00f3 el movimiento." }); await load();
    } else {
      failed(result.code);
      if (result.code === "invalid_category" || result.code === "reserved_category" || result.code === "not_found") {
        setCategoryError(errorMessage(result.code, "finance")); categoryRef.current?.focus();
      } else form.failed(result.code);
    }
    pending.current = false; setBusy(false);
  }

  async function reverse(target: Entry) {
    setConfirmReverse(null);
    if (pending.current) return;
    pending.current = true; setBusy(true); setStatus(null);
    const result = await call(apiClient.POST("/finance/entries/{entryId}/reversal", { params: { path: { entryId: target.id } } }));
    if (result.ok) { setStatus({ tone: "ok", text: "Se revirtió el movimiento." }); await load(); } else failed(result.code);
    pending.current = false; setBusy(false);
  }

  return <>
    <header className="page-header"><PageTitle>Caja</PageTitle></header>
    <FormStatus status={status} {...form.statusProps} />
    <section className="panel"><h2>Saldo</h2><p>{balance === null ? "Cargando..." : pesos(balance)}</p></section>
    {hasRole(me, "officer") && <section className="panel">
      {!open ? <button type="button" className="button button--primary" onClick={() => setOpen(true)}>Nuevo movimiento</button> : <>
        <h2>Nuevo movimiento</h2>
        <form {...form.formProps} onSubmit={(event) => { event.preventDefault(); void save(); }}>
          <div className="field"><label htmlFor="finance-direction">Tipo</label><select id="finance-direction" value={direction} onChange={(event) => { setDirection(event.target.value); setCategory(""); }}><option value="income">Ingreso</option><option value="expense">Gasto</option></select></div>
          <div className="field"><label htmlFor="finance-category">{"Categor\u00eda"}</label><select id="finance-category" ref={categoryRef} value={category} aria-invalid={!!categoryError || undefined} aria-describedby={categoryError ? "finance-category-error" : undefined} onChange={(event) => { setCategory(event.target.value); setCategoryError(""); }}><option value="">{"Eleg\u00ed una categor\u00eda"}</option>{categories.filter((c) => c.active && !c.reserved && c.direction === direction).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</select>{categoryError && <p id="finance-category-error" role="alert">{categoryError}</p>}</div>
          <Field label="Fecha" type="date" {...form.field("date")} value={date} onChange={(event) => setDate(event.target.value)} />
          <Field label="Monto en pesos" inputMode="decimal" {...form.field("amount")} value={amount} onChange={(event) => setAmount(event.target.value)} />
          <Field label={"Descripci\u00f3n"} {...form.field("description")} value={description} onChange={(event) => setDescription(event.target.value)} />
          <button className="button button--primary" disabled={busy}>Guardar</button>{" "}<button type="button" className="button button--secondary" disabled={busy} onClick={() => setOpen(false)}>Cancelar</button>
        </form>
      </>}
    </section>}
    <section className="panel"><h2>Movimientos</h2>
      {entries === null ? <p>Cargando...</p> : entries.length === 0 ? <p>{"Todav\u00eda no hay movimientos."}</p> : <ul>{entries.map((entry) => <li key={entry.id}>
        <strong>{entry.description}</strong> {"\u2014"} {pesos(entry.amountCents)} ({entry.direction === "income" ? "Ingreso" : "Gasto"})<br />
        {categories.find((c) => c.id === entry.categoryId)?.name} {"\u2014"} {entry.date}<br />
        Registrado por {entry.recordedBy} {"\u2014"} {new Date(entry.recordedAt).toLocaleString("es-AR", { timeZone: "America/Argentina/Buenos_Aires" })}
        {entry.reversalId && <><br /><span>Revertido por {entry.reversedBy} el {dayMonthYear(entry.reversedOn ?? "")}</span></>}
        {hasRole(me, "officer") && !entry.reversalId && !entry.reversesId && <><br /><button type="button" className="button button--secondary button--small" disabled={busy} aria-label={`Revertir ${entry.description}`} onClick={() => setConfirmReverse(entry)}>Revertir</button></>}
      </li>)}</ul>}
    </section>
    {confirmReverse && <ConfirmDialog title="Revertir movimiento" confirmLabel="Revertir" onConfirm={() => void reverse(confirmReverse)} onCancel={() => setConfirmReverse(null)}>
      <p>{"¿Revertís este movimiento? Se va a cargar un movimiento opuesto con fecha de hoy."}</p>
    </ConfirmDialog>}
  </>;
}
