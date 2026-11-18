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
import { canReadContacts, hasRole, useMe, useSession } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { centsOf, dayMonthYear, pesos, todayArgentina } from "../lib/money.ts";
import { FinanceCategories } from "./FinanceCategories.tsx";

type Category = components["schemas"]["FinanceCategory"];
type Entry = Omit<components["schemas"]["FinanceEntry"], "amountCents"> & { amountCents: string };
type EntryDetail = { entry: Entry; receipts: FinanceReceipt[] };
const today = todayArgentina;
const MAX_RECEIPT_BYTES = 10 * 1024 * 1024;
const ACCEPTED_RECEIPTS = ".jpg,.jpeg,.png,.pdf,image/jpeg,image/png,application/pdf";
const R5_UPLOAD_FAILED = "El movimiento se guard\u00f3, pero el comprobante no. Volv\u00e9 a subirlo desde el detalle.";

function receiptTypeLabel(contentType: FinanceReceipt["contentType"]): string {
  switch (contentType) {
    case "image/jpeg":
      return "JPG";
    case "image/png":
      return "PNG";
    case "application/pdf":
      return "PDF";
  }
}

function receiptExt(contentType: FinanceReceipt["contentType"]): string {
  switch (contentType) {
    case "image/jpeg":
      return "jpg";
    case "image/png":
      return "png";
    case "application/pdf":
      return "pdf";
  }
}

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
  const canViewReceipts = canReadContacts(me);
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
  const [receiptFile, setReceiptFile] = useState<File | null>(null);
  const [detail, setDetail] = useState<EntryDetail | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  const categoryRef = useRef<HTMLSelectElement>(null);
  const [categoryError, setCategoryError] = useState("");
  const [confirmReverse, setConfirmReverse] = useState<Entry | null>(null);
  const form = useFormErrors();

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

  async function save() {
    if (pending.current) return;
    if (!category) {
      setCategoryError("Eleg\u00ed una categor\u00eda.");
      categoryRef.current?.focus();
      return;
    }
    if (!form.validate({
      date: (v) => (!v ? "Eleg\u00ed una fecha." : null),
      amount: (v) => (centsOf(v) === null ? "Ingres\u00e1 un monto positivo con hasta dos decimales." : null),
      description: (v) =>
        !v.trim()
          ? "Escrib\u00ed una descripci\u00f3n."
          : Array.from(v.trim()).length > 500
            ? "La descripci\u00f3n puede tener hasta 500 caracteres."
            : null,
    })) return;
    pending.current = true;
    setBusy(true);
    setStatus(null);
    const pendingFile = receiptFile;
    const result = await createFinanceEntry({
      categoryId: category,
      direction,
      date,
      amountCents: centsOf(amount)!,
      description,
    });
    if (result.ok) {
      setOpen(false);
      setAmount("");
      setDescription("");
      setCategory("");
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
      if (result.code === "invalid_category" || result.code === "reserved_category" || result.code === "not_found") {
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
    if (file.size === 0) {
      failed("empty_file");
      form.failed("empty_file");
      return;
    }
    if (file.size > MAX_RECEIPT_BYTES) {
      failed("file_too_large");
      form.failed("file_too_large");
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
      failed(res.code);
      form.failed(res.code);
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
    <FormStatus status={status} {...form.statusProps} />
    <button type="button" className="button button--secondary" onClick={() => setShowCategories(!showCategories)}>{"Categor\u00edas"}</button>
    {showCategories && <FinanceCategories categories={categories} used={entries === null ? null : new Set(entries.map((e) => e.categoryId))} officer={isOfficer} reload={load} authError={(code) => { if (code === "mfa_required" || code === "unauthenticated") void refresh(); }} />}
    <section className="panel"><h2>Saldo</h2><p>{balance === null ? "Cargando..." : pesos(balance)}</p></section>
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
          <button className="button button--primary" disabled={busy}>Guardar</button>{" "}<button type="button" className="button button--secondary" disabled={busy} onClick={() => { setOpen(false); setReceiptFile(null); }}>Cancelar</button>
        </form>
      </>}
    </section>}
    <section className="panel"><h2>Movimientos</h2>
      {entries === null ? <p>Cargando...</p> : entries.length === 0 ? <p>{"Todav\u00eda no hay movimientos."}</p> : <ul>{entries.map((entry) => <li key={entry.id}>
        <strong>{entry.description}</strong> {"\u2014"} {pesos(entry.amountCents)} ({entry.direction === "income" ? "Ingreso" : "Gasto"})<br />
        {categories.find((c) => c.id === entry.categoryId)?.name} {"\u2014"} {entry.date}<br />
        Registrado por {entry.recordedBy} {"\u2014"} {new Date(entry.recordedAt).toLocaleString("es-AR", { timeZone: "America/Argentina/Buenos_Aires" })}
        {entry.hasReceipt && <><br /><span>Con comprobante</span></>}
        {entry.reversalId && <><br /><span>Revertido por {entry.reversedBy} el {dayMonthYear(entry.reversedOn ?? "")}</span></>}
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
              {pesos(detail.entry.amountCents)} ({detail.entry.direction === "income" ? "Ingreso" : "Gasto"}) —{" "}
              {categories.find((c) => c.id === detail.entry.categoryId)?.name} — {detail.entry.date}
            </p>
          </div>
          <button type="button" className="button button--ghost button--small" onClick={() => setDetail(null)}>
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
                  <strong>Comprobante {index + 1}</strong> — {receiptTypeLabel(receipt.contentType)} ({formatSize(receipt.sizeBytes)})
                </div>
                <a
                  className="button button--secondary button--small"
                  href={`/api/finance/entries/${detail.entry.id}/receipts/${receipt.id}`}
                  download={`comprobante-${receipt.id}.${receiptExt(receipt.contentType)}`}
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
              id={detailFileId}
              className="file-input"
              type="file"
              accept={ACCEPTED_RECEIPTS}
              disabled={busy}
              aria-describedby={`${detailFileId}-hint`}
              onChange={(event) => {
                const file = event.target.files?.[0];
                event.target.value = "";
                if (file) void uploadFromDetail(detail.entry, file);
              }}
            />
            <label htmlFor={detailFileId} className="button button--secondary file-button">
              Subir comprobante
            </label>
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
  </>;
}
