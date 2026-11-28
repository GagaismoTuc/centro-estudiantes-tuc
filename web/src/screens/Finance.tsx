import { useCallback, useEffect, useId, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import {
  createFinanceEntry,
  entryReceipts,
  financeBalance,
  financeEntries,
  uploadEntryReceipt,
  type FinanceReceipt,
} from "../api/finance.ts";
import type { components } from "../api/schema.gen.ts";
import { canReadReceipts, hasRole, useMe, useSession } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { Link } from "../lib/router.tsx";
import { formatDate, formatDateTime, todayArgentina } from "../lib/datetime.ts";
import { formatPesos, parsePesos } from "../lib/money.ts";
import { FinanceCategories } from "./FinanceCategories.tsx";

type Category = components["schemas"]["FinanceCategory"];
type EventBudget = components["schemas"]["EventBudget"];
type Entry = Omit<components["schemas"]["FinanceEntry"], "amountCents"> & { amountCents: string };
type EntryDetail = { entry: Entry; receipts: FinanceReceipt[] };
type EventHeadingOption = { id: string; name: string };
const today = todayArgentina;
const MAX_RECEIPT_BYTES = 10 * 1024 * 1024;
const ACCEPTED_RECEIPTS = ".jpg,.jpeg,.png,.pdf,image/jpeg,image/png,application/pdf";
const R5_UPLOAD_FAILED = "El movimiento se guard\u00f3, pero el comprobante no. Volv\u00e9 a subirlo desde el detalle.";

const receiptFormats: Record<FinanceReceipt["contentType"], { label: string; ext: string }> = {
  "image/jpeg": { label: "JPG", ext: "jpg" },
  "image/png": { label: "PNG", ext: "png" },
  "application/pdf": { label: "PDF", ext: "pdf" },
};

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  if (kb < 1024) return `${Math.round(kb)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function Finance() {
  useDocumentTitle("Caja");
  const me = useMe();
  const { refresh } = useSession();
  const isOfficer = hasRole(me, "officer");
  const canViewReceipts = canReadReceipts(me);
  const createFileId = useId();
  const detailFileId = useId();

  const [entries, setEntries] = useState<Entry[] | null>(null);
  const [balance, setBalance] = useState<string | null>(null);
  const [categories, setCategories] = useState<Category[]>([]);
  const [open, setOpen] = useState(false);
  const [showCategories, setShowCategories] = useState(false);
  const [direction, setDirection] = useState("income");
  const [category, setCategory] = useState("");
  const [date, setDate] = useState(today);
  const [amount, setAmount] = useState("");
  const [description, setDescription] = useState("");
  const [eventBudgets, setEventBudgets] = useState<EventBudget[]>([]);
  const [eventBudgetId, setEventBudgetId] = useState("");
  const [eventHeadings, setEventHeadings] = useState<EventHeadingOption[]>([]);
  const [eventHeadingsLoading, setEventHeadingsLoading] = useState(false);
  const [headingId, setHeadingId] = useState("");
  const [receiptFile, setReceiptFile] = useState<File | null>(null);
  const [detail, setDetail] = useState<EntryDetail | null>(null);
  const [detailReceiptError, setDetailReceiptError] = useState("");
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  const categoryRef = useRef<HTMLSelectElement>(null);
  const eventBudgetRef = useRef<HTMLSelectElement>(null);
  const headingRef = useRef<HTMLSelectElement>(null);
  const detailFileRef = useRef<HTMLInputElement>(null);
  const focusDetailFile = useRef(false);
  const focusEventField = useRef<"event" | "heading" | null>(null);
  const [categoryError, setCategoryError] = useState("");
  const [eventError, setEventError] = useState("");
  const [headingError, setHeadingError] = useState("");
  const [confirmReverse, setConfirmReverse] = useState<Entry | null>(null);
  /** Balance in centavos an expense would leave, while its confirmation is open. */
  const [confirmOverdraft, setConfirmOverdraft] = useState<string | null>(null);
  const form = useFormErrors();

  useEffect(() => {
    if (!focusEventField.current || busy) return;
    if (focusEventField.current === "heading" && eventHeadingsLoading) return;
    const target = focusEventField.current;
    focusEventField.current = null;
    if (target === "heading") headingRef.current?.focus();
    else eventBudgetRef.current?.focus();
  }, [busy, eventHeadingsLoading, eventError, headingError]);

  useEffect(() => {
    if (!isOfficer || !open) return;
    let cancelled = false;
    void call(apiClient.GET("/event-budgets")).then((res) => {
      if (cancelled) return;
      if (res.ok) setEventBudgets(res.data.filter((b) => !b.archived));
    });
    return () => { cancelled = true; };
  }, [isOfficer, open]);

  useEffect(() => {
    if (!eventBudgetId) {
      setEventHeadings([]);
      setEventHeadingsLoading(false);
      return;
    }
    let cancelled = false;
    setEventHeadings([]);
    setEventHeadingsLoading(true);
    void call(apiClient.GET("/event-budgets/{id}", { params: { path: { id: eventBudgetId } } })).then((res) => {
      if (cancelled) return;
      setEventHeadingsLoading(false);
      if (res.ok) {
        const usedIds = new Set(res.data.lines.map((l) => l.headingId).filter((id): id is string => Boolean(id)));
        const options = (res.data.byHeading ?? [])
          .filter((h) => usedIds.has(h.headingId))
          .map((h) => ({ id: h.headingId, name: h.headingName }));
        setEventHeadings(options);
      } else {
        setEventHeadings([]);
        setEventError(errorMessage(res.code, "finance"));
      }
    });
    return () => { cancelled = true; };
  }, [eventBudgetId]);

  useEffect(() => {
    if (!focusDetailFile.current || busy || !detailReceiptError) return;
    focusDetailFile.current = false;
    detailFileRef.current?.focus();
  }, [busy, detailReceiptError]);

  const failed = useCallback((code: string) => {
    if (code === "mfa_required" || code === "unauthenticated") void refresh();
    setStatus({ tone: "error", text: errorMessage(code, "finance") });
  }, [refresh]);

  const load = useCallback(async () => {
    const [list, total, cats] = await Promise.all([
      financeEntries(),
      financeBalance(),
      call(apiClient.GET("/finance/categories")),
    ]);
    if (list.ok) {
      setEntries(list.data);
      setDetail((prev) => {
        if (!prev) return null;
        const fresh = list.data.find((item) => item.id === prev.entry.id);
        return fresh ? { ...prev, entry: fresh } : null;
      });
    } else failed(list.code);
    if (total.ok) setBalance(total.data.balanceCents);
    else failed(total.code);
    if (cats.ok) setCategories(cats.data);
    else failed(cats.code);
  }, [failed]);

  useEffect(() => {
    void load();
  }, [load]);

  async function save(overdraftConfirmed = false) {
    if (pending.current) return;
    if (!category) {
      setCategoryError("Eleg\u00ed una categor\u00eda.");
      categoryRef.current?.focus();
      return;
    }
    if (!form.validate({
      date: (v) => (!v ? "Eleg\u00ed una fecha." : null),
      amount: (v) => (parsePesos(v) === null ? "Ingres\u00e1 un monto positivo con hasta dos decimales." : null),
      description: (v) =>
        !v.trim()
          ? "Escrib\u00ed una descripci\u00f3n."
          : Array.from(v.trim()).length > 500
            ? "La descripci\u00f3n puede tener hasta 500 caracteres."
            : null,
    })) return;
    if (eventBudgetId && !headingId) {
      focusEventField.current = "heading";
      setHeadingError("Eleg\u00ed un rubro del evento.");
      if (!eventHeadingsLoading) headingRef.current?.focus();
      return;
    }
    if (!overdraftConfirmed && direction === "expense" && balance !== null) {
      // #135: an expense may still overdraw the fund, but only after the officer confirms it.
      // shortcut: no warning while the balance is unknown (not loaded or failed); the server allows it anyway.
      const after = BigInt(balance) - parsePesos(amount)!;
      if (after < 0n) {
        setConfirmOverdraft(String(after));
        return;
      }
    }
    pending.current = true;
    setBusy(true);
    setStatus(null);
    const pendingFile = receiptFile;
    const result = await createFinanceEntry({
      categoryId: category,
      direction,
      date,
      amountCents: String(parsePesos(amount)!),
      description,
      ...(eventBudgetId ? { eventBudgetId, headingId } : {}),
    });
    if (result.ok) {
      setOpen(false);
      setAmount("");
      setDescription("");
      setCategory("");
      setEventBudgetId("");
      setHeadingId("");
      setEventHeadings([]);
      setEventError("");
      setHeadingError("");
      setReceiptFile(null);
      if (pendingFile) {
        const uploaded = await uploadEntryReceipt(result.data.id, pendingFile);
        if (uploaded.ok) {
          setStatus({ tone: "ok", text: "Se registr\u00f3 el movimiento." });
        } else {
          if (uploaded.code === "mfa_required" || uploaded.code === "unauthenticated") void refresh();
          setStatus({ tone: "error", text: R5_UPLOAD_FAILED });
          setDetail({ entry: result.data, receipts: [] });
          form.failed(uploaded.code);
        }
      } else {
        setStatus({ tone: "ok", text: "Se registr\u00f3 el movimiento." });
      }
      await load();
    } else {
      failed(result.code);
      if (result.code === "heading_not_in_budget") {
        focusEventField.current = "heading";
        setHeadingError(errorMessage(result.code, "finance"));
      } else if (result.code === "invalid_event_link") {
        focusEventField.current = "event";
        setEventError(errorMessage(result.code, "finance"));
      } else if (result.code === "invalid_category" || result.code === "reserved_category" || (result.code === "not_found" && !eventBudgetId)) {
        setCategoryError(errorMessage(result.code, "finance"));
        categoryRef.current?.focus();
      } else form.failed(result.code);
    }
    pending.current = false;
    setBusy(false);
  }

  async function openDetail(target: Entry) {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setStatus(null);
    setDetailReceiptError("");
    const res = await entryReceipts(target.id);
    if (res.ok) {
      setDetail({ entry: target, receipts: res.data });
    } else {
      failed(res.code);
      form.failed(res.code);
    }
    pending.current = false;
    setBusy(false);
  }

  async function uploadFromDetail(target: Entry, file: File) {
    if (pending.current) return;
    setDetailReceiptError("");
    const failDetailReceipt = (code: string) => {
      if (code === "mfa_required" || code === "unauthenticated") void refresh();
      focusDetailFile.current = true;
      setDetailReceiptError(errorMessage(code, "finance"));
    };
    if (file.size === 0) {
      failDetailReceipt("empty_file");
      return;
    }
    if (file.size > MAX_RECEIPT_BYTES) {
      failDetailReceipt("file_too_large");
      return;
    }
    pending.current = true;
    setBusy(true);
    setStatus(null);
    const res = await uploadEntryReceipt(target.id, file);
    if (res.ok) {
      setStatus({ tone: "ok", text: "Se subi\u00f3 el comprobante." });
      const list = await entryReceipts(target.id);
      if (list.ok) {
        setDetail((prev) =>
          prev && prev.entry.id === target.id
            ? { entry: { ...prev.entry, hasReceipt: true }, receipts: list.data }
            : prev,
        );
      }
      await load();
    } else {
      failDetailReceipt(res.code);
    }
    pending.current = false;
    setBusy(false);
  }

  async function reverse(target: Entry) {
    setConfirmReverse(null);
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setStatus(null);
    const result = await call(apiClient.POST("/finance/entries/{entryId}/reversal", { params: { path: { entryId: target.id } } }));
    if (result.ok) {
      setStatus({ tone: "ok", text: "Se revirtió el movimiento." });
      await load();
    } else {
      failed(result.code);
      form.failed(result.code);
    }
    pending.current = false;
    setBusy(false);
  }

  return <>
    <header className="page-header"><PageTitle>Caja</PageTitle></header>
    <div className="page-actions">
      <Link className="button button--secondary" to="/caja/informe">Informe</Link>
      <Link className="button button--secondary" to="/caja/rendiciones">Rendiciones</Link>
      <a className="button button--secondary" href="/api/finance/entries.csv" download>Descargar CSV</a>
    </div>
    <FormStatus status={status} {...form.statusProps} />
    <button type="button" className="button button--secondary" onClick={() => setShowCategories(!showCategories)}>{"Categor\u00edas"}</button>
    {showCategories && <FinanceCategories categories={categories} used={entries === null ? null : new Set(entries.map((e) => e.categoryId))} officer={isOfficer} reload={load} authError={(code) => { if (code === "mfa_required" || code === "unauthenticated") void refresh(); }} />}
    <section className="panel"><h2>Saldo</h2><p>{balance === null ? "Cargando..." : formatPesos(balance)}</p></section>
    {isOfficer && <section className="panel">
      {!open ? <button type="button" className="button button--primary" onClick={() => setOpen(true)}>Nuevo movimiento</button> : <>
        <h2>Nuevo movimiento</h2>
        <form {...form.formProps} onSubmit={(event) => { event.preventDefault(); void save(); }}>
          <div className="field"><label htmlFor="finance-direction">Tipo</label><select id="finance-direction" value={direction} onChange={(event) => { setDirection(event.target.value); setCategory(""); }}><option value="income">Ingreso</option><option value="expense">Gasto</option></select></div>
          <div className="field"><label htmlFor="finance-category">{"Categor\u00eda"}</label><select id="finance-category" ref={categoryRef} value={category} aria-invalid={!!categoryError || undefined} aria-describedby={categoryError ? "finance-category-error" : undefined} onChange={(event) => { setCategory(event.target.value); setCategoryError(""); }}><option value="">{"Eleg\u00ed una categor\u00eda"}</option>{categories.filter((c) => c.active && !c.reserved && c.direction === direction).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</select>{categoryError && <p id="finance-category-error" role="alert">{categoryError}</p>}</div>
          <Field label="Fecha" type="date" {...form.field("date")} value={date} onChange={(event) => setDate(event.target.value)} />
          <Field label="Monto en pesos" inputMode="decimal" {...form.field("amount")} value={amount} onChange={(event) => setAmount(event.target.value)} />
          <Field label={"Descripci\u00f3n"} {...form.field("description")} value={description} onChange={(event) => setDescription(event.target.value)} />
          <div className="field">
            <label htmlFor="finance-event-budget">Evento (opcional)</label>
            <select
              id="finance-event-budget"
              ref={eventBudgetRef}
              value={eventBudgetId}
              disabled={busy}
              aria-invalid={!!eventError || undefined}
              aria-describedby={eventError ? "finance-event-budget-error" : undefined}
              onChange={(event) => {
                setEventBudgetId(event.target.value);
                setHeadingId("");
                setEventError("");
                setHeadingError("");
              }}
            >
              <option value="">Sin evento</option>
              {eventBudgets.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}
            </select>
            {eventError && <p id="finance-event-budget-error" role="alert">{eventError}</p>}
          </div>
          {eventBudgetId && (
            <div className="field">
              <label htmlFor="finance-heading">Rubro del evento</label>
              <select
                id="finance-heading"
                ref={headingRef}
                value={headingId}
                disabled={busy || eventHeadingsLoading}
                aria-invalid={!!headingError || undefined}
                aria-describedby={headingError ? "finance-heading-error" : undefined}
                onChange={(event) => {
                  setHeadingId(event.target.value);
                  setHeadingError("");
                }}
              >
                <option value="">{eventHeadingsLoading ? "Cargando rubros..." : "Eleg\u00ed un rubro"}</option>
                {eventHeadings.map((h) => <option key={h.id} value={h.id}>{h.name}</option>)}
              </select>
              {headingError && <p id="finance-heading-error" role="alert">{headingError}</p>}
              {!eventHeadingsLoading && eventHeadings.length === 0 && (
                <p className="field__hint">{"Este presupuesto todav\u00eda no tiene rubros con l\u00edneas."}</p>
              )}
            </div>
          )}
          <div className="field">
            <input
              id={createFileId}
              className="file-input"
              type="file"
              accept={ACCEPTED_RECEIPTS}
              disabled={busy}
              aria-describedby={`${createFileId}-name ${createFileId}-hint`}
              onChange={(event) => setReceiptFile(event.target.files?.[0] ?? null)}
            />
            <label htmlFor={createFileId} className="button button--secondary file-button">
              Comprobante (opcional)
            </label>
            <p className="file-name small" id={`${createFileId}-name`}>
              {receiptFile ? receiptFile.name : "Ning\u00fan archivo elegido"}
            </p>
            <p className="field__hint" id={`${createFileId}-hint`}>
              JPG, PNG o PDF de hasta 10 MB.
            </p>
          </div>
          <button className="button button--primary" disabled={busy}>Guardar</button>{" "}<button type="button" className="button button--secondary" disabled={busy} onClick={() => { setOpen(false); setEventBudgetId(""); setHeadingId(""); setEventHeadings([]); setEventError(""); setHeadingError(""); setReceiptFile(null); }}>Cancelar</button>
        </form>
      </>}
    </section>}
    <section className="panel"><h2>Movimientos</h2>
      {entries === null ? <p>Cargando...</p> : entries.length === 0 ? <p>{"Todav\u00eda no hay movimientos."}</p> : <ul>{entries.map((entry) => <li key={entry.id}>
        <strong>{entry.description}</strong> {"\u2014"} <span className="amount">{formatPesos(entry.amountCents)}</span> ({entry.direction === "income" ? "Ingreso" : "Gasto"})<br />
        {categories.find((c) => c.id === entry.categoryId)?.name} {"\u2014"} {formatDate(entry.date)}<br />
        Registrado por {entry.recordedBy} {"\u2014"} {formatDateTime(entry.recordedAt)}
        {entry.eventBudgetName && <><br /><span>Evento: {entry.eventBudgetName}{entry.headingName ? ` \u2014 ${entry.headingName}` : ""}</span></>}
        {entry.hasReceipt && <><br /><span>Con comprobante</span></>}
        {entry.reversalId && <><br /><span>Revertido por {entry.reversedBy} el {formatDate(entry.reversedOn ?? "")}</span></>}
        {(canViewReceipts || (isOfficer && !entry.reversalId && !entry.reversesId)) && (
          <div className="panel__actions">
            {canViewReceipts && (
              <button
                type="button"
                className="button button--secondary button--small"
                disabled={busy}
                aria-expanded={detail?.entry.id === entry.id}
                aria-label={`Ver detalle de ${entry.description}`}
                onClick={() => void openDetail(entry)}
              >
                Ver detalle
              </button>
            )}
            {isOfficer && !entry.reversalId && !entry.reversesId && (
              <button
                type="button"
                className="button button--secondary button--small"
                disabled={busy}
                aria-label={`Revertir ${entry.description}`}
                onClick={() => setConfirmReverse(entry)}
              >
                Revertir
              </button>
            )}
          </div>
        )}
      </li>)}</ul>}
    </section>
    {canViewReceipts && detail && (
      <section className="panel" aria-labelledby="entry-detail-title">
        <div className="panel__header">
          <div>
            <h2 id="entry-detail-title">{detail.entry.description}</h2>
            <p className="muted small">
              {formatPesos(detail.entry.amountCents)} ({detail.entry.direction === "income" ? "Ingreso" : "Gasto"}) —{" "}
              {categories.find((c) => c.id === detail.entry.categoryId)?.name} — {formatDate(detail.entry.date)}
              {detail.entry.eventBudgetName && <> — Evento: {detail.entry.eventBudgetName}{detail.entry.headingName ? ` (${detail.entry.headingName})` : ""}</>}
            </p>
          </div>
          <button type="button" className="button button--ghost button--small" onClick={() => { setDetail(null); setDetailReceiptError(""); }}>
            Cerrar detalle
          </button>
        </div>
        <h3>Comprobantes</h3>
        {detail.receipts.length === 0 ? (
          <p>{"Todav\u00eda no hay comprobantes."}</p>
        ) : (
          <ul className="plain-list">
            {detail.receipts.map((receipt, index) => (
              <li key={receipt.id}>
                <div>
                  <strong>Comprobante {index + 1}</strong> — {receiptFormats[receipt.contentType].label} ({formatSize(receipt.sizeBytes)})
                </div>
                <a
                  className="button button--secondary button--small"
                  href={`/api/finance/entries/${detail.entry.id}/receipts/${receipt.id}`}
                  download={`comprobante-${receipt.id}.${receiptFormats[receipt.contentType].ext}`}
                >
                  Descargar comprobante {index + 1}
                </a>
              </li>
            ))}
          </ul>
        )}
        {isOfficer && (
          <div className="field">
            <input
              ref={detailFileRef}
              id={detailFileId}
              className="file-input"
              type="file"
              accept={ACCEPTED_RECEIPTS}
              disabled={busy}
              aria-invalid={!!detailReceiptError || undefined}
              aria-describedby={detailReceiptError ? `${detailFileId}-error ${detailFileId}-hint` : `${detailFileId}-hint`}
              onChange={(event) => {
                const file = event.target.files?.[0];
                event.target.value = "";
                if (file) void uploadFromDetail(detail.entry, file);
              }}
            />
            <label htmlFor={detailFileId} className="button button--secondary file-button">
              Subir comprobante
            </label>
            {detailReceiptError && (
              <p id={`${detailFileId}-error`} role="alert">
                {detailReceiptError}
              </p>
            )}
            <p className="field__hint" id={`${detailFileId}-hint`}>
              {"JPG, PNG o PDF de hasta 10 MB. Pod\u00e9s sumar m\u00e1s de uno; no se reemplazan ni se borran."}
            </p>
          </div>
        )}
      </section>
    )}
    {confirmReverse && <ConfirmDialog title="Revertir movimiento" confirmLabel="Revertir" onConfirm={() => void reverse(confirmReverse)} onCancel={() => setConfirmReverse(null)}>
      <p>{"¿Revertís este movimiento? Se va a cargar un movimiento opuesto con fecha de hoy."}</p>
    </ConfirmDialog>}
    {confirmOverdraft !== null && <ConfirmDialog title="Saldo negativo" confirmLabel="Registrar igual" onConfirm={() => { setConfirmOverdraft(null); void save(true); }} onCancel={() => setConfirmOverdraft(null)}>
      <p>El saldo va a quedar en <span className="amount">{formatPesos(confirmOverdraft)}</span>. ¿Registrar igual?</p>
    </ConfirmDialog>}
  </>;
}
