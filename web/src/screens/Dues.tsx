import { useCallback, useEffect, useId, useRef, useState } from "react";
import { createDue, deleteDue, getDue, listDues, listMyDues, updateDue, type DueDetail, type DueSummary, type MyDue } from "../api/dues.ts";
import { errorMessage } from "../api/errors.ts";
import { canReadDues, hasRole, useMe, useSession } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { centsOf, dayMonthYear, pesos, pesosInput } from "../lib/money.ts";

const memberStatusLabel: Record<string, string> = {
  owing: "Debe",
  paid: "Pagó",
  exempt: "Exento",
};

function myDueText(due: MyDue): string {
  const formatted = pesos(due.amountCents);
  const formattedDate = dayMonthYear(due.dueDate);
  if (due.status === "paid") return `Pagada ${formatted} — vencía el ${formattedDate}`;
  if (due.status === "exempt") return `Exenta ${formatted} — vencía el ${formattedDate}`;
  return `Debés ${formatted} — vence el ${formattedDate}`;
}

type DueFormProps = {
  heading: string;
  submitLabel: string;
  busy: boolean;
  initial?: { name: string; amount: string; dueDate: string };
  form: ReturnType<typeof useFormErrors>;
  onSubmit: (values: { name: string; amountCents: string; dueDate: string }) => void;
  onCancel: () => void;
};

function DueForm({ heading, submitLabel, busy, initial, form, onSubmit, onCancel }: DueFormProps) {
  const headingId = useId();
  const [name, setName] = useState(initial?.name ?? "");
  const [amount, setAmount] = useState(initial?.amount ?? "");
  const [dueDate, setDueDate] = useState(initial?.dueDate ?? "");

  function handleSubmit() {
    if (!form.validate({
      name: (v) => (!v.trim() ? "Escribí un nombre." : null),
      amount: (v) => (centsOf(v) === null ? "Ingresá un monto positivo con hasta dos decimales." : null),
      dueDate: (v) => (!v ? "Elegí una fecha de vencimiento." : null),
    })) {
      return;
    }
    onSubmit({ name: name.trim(), amountCents: centsOf(amount)!, dueDate });
  }

  return (
    <section className="panel">
      <h2 id={headingId}>{heading}</h2>
      <form
        aria-labelledby={headingId}
        {...form.formProps}
        onSubmit={(event) => {
          event.preventDefault();
          handleSubmit();
        }}
      >
        <Field label="Nombre" {...form.field("name")} value={name} onChange={(e) => setName(e.target.value)} />
        <Field label="Monto en pesos" inputMode="decimal" {...form.field("amount")} value={amount} onChange={(e) => setAmount(e.target.value)} />
        <Field label="Vencimiento" type="date" {...form.field("dueDate")} value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
        <div className="panel__actions">
          <button type="submit" className="button button--primary" disabled={busy}>
            {submitLabel}
          </button>
          <button type="button" className="button button--secondary" disabled={busy} onClick={onCancel}>
            Cancelar
          </button>
        </div>
      </form>
    </section>
  );
}

export function Dues() {
  const me = useMe();
  const { refresh } = useSession();
  const canReadFull = canReadDues(me);
  const isOfficer = hasRole(me, "officer");
  const showMine = hasRole(me, "student") || isOfficer;
  useDocumentTitle(canReadFull ? "Cuotas" : "Mis cuotas");

  const [dues, setDues] = useState<DueSummary[] | null>(null);
  const [mine, setMine] = useState<MyDue[] | null>(null);
  const [detail, setDetail] = useState<DueDetail | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [editing, setEditing] = useState<DueSummary | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<DueSummary | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  const createForm = useFormErrors();
  const editForm = useFormErrors();

  const failed = useCallback(
    (code: string, targetForm?: ReturnType<typeof useFormErrors>) => {
      if (code === "mfa_required" || code === "unauthenticated") void refresh();
      setStatus({ tone: "error", text: errorMessage(code) });
      targetForm?.failed(code);
    },
    [refresh],
  );

  const load = useCallback(async () => {
    const [fullResult, mineResult] = await Promise.all([
      canReadFull ? listDues() : Promise.resolve(null),
      showMine ? listMyDues() : Promise.resolve(null),
    ]);
    if (fullResult) {
      if (fullResult.ok) setDues(fullResult.data);
      else failed(fullResult.code);
    }
    if (mineResult) {
      if (mineResult.ok) setMine(mineResult.data);
      else failed(mineResult.code);
    }
  }, [canReadFull, showMine, failed]);

  useEffect(() => {
    void load();
  }, [load]);

  async function handleCreate(values: { name: string; amountCents: string; dueDate: string }) {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setStatus(null);
    const result = await createDue(values);
    if (result.ok) {
      setShowCreate(false);
      setDetail(result.data);
      setStatus({ tone: "ok", text: "Se creó la cuota." });
      await load();
    } else {
      failed(result.code, createForm);
    }
    pending.current = false;
    setBusy(false);
  }

  async function handleEdit(values: { name: string; amountCents: string; dueDate: string }) {
    if (!editing || pending.current) return;
    pending.current = true;
    setBusy(true);
    setStatus(null);
    const result = await updateDue(editing.id, values);
    if (result.ok) {
      setEditing(null);
      if (detail?.id === result.data.id) setDetail(result.data);
      setStatus({ tone: "ok", text: "Se actualizó la cuota." });
      await load();
    } else {
      failed(result.code, editForm);
    }
    pending.current = false;
    setBusy(false);
  }

  async function handleDelete(target: DueSummary) {
    setConfirmDelete(null);
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setStatus(null);
    const result = await deleteDue(target.id);
    if (result.ok) {
      if (detail?.id === target.id) setDetail(null);
      if (editing?.id === target.id) setEditing(null);
      setStatus({ tone: "ok", text: "Se borró la cuota." });
      await load();
    } else {
      failed(result.code);
    }
    pending.current = false;
    setBusy(false);
  }

  async function openDetail(id: string) {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    const result = await getDue(id);
    if (result.ok) setDetail(result.data);
    else failed(result.code);
    pending.current = false;
    setBusy(false);
  }

  const activeForm = editing ? editForm : createForm;

  return (
    <>
      <header className="page-header">
        <PageTitle>{canReadFull ? "Cuotas" : "Mis cuotas"}</PageTitle>
        {isOfficer && !showCreate && (
          <div className="page-actions">
            <button
              type="button"
              className="button button--primary"
              onClick={() => {
                setEditing(null);
                createForm.clear();
                setStatus(null);
                setShowCreate(true);
              }}
            >
              Nueva cuota
            </button>
          </div>
        )}
      </header>

      <FormStatus status={status} {...activeForm.statusProps} />

      {isOfficer && showCreate && (
        <DueForm
          heading="Nueva cuota"
          submitLabel="Guardar"
          busy={busy}
          form={createForm}
          onSubmit={(values) => void handleCreate(values)}
          onCancel={() => {
            setShowCreate(false);
            createForm.clear();
          }}
        />
      )}

      {isOfficer && editing && (
        <DueForm
          key={editing.id}
          heading="Editar cuota"
          submitLabel="Guardar cambios"
          busy={busy}
          initial={{ name: editing.name, amount: pesosInput(editing.amountCents), dueDate: editing.dueDate }}
          form={editForm}
          onSubmit={(values) => void handleEdit(values)}
          onCancel={() => {
            setEditing(null);
            editForm.clear();
          }}
        />
      )}

      {canReadFull && (
        <section className="panel">
          <h2>Listado de cuotas</h2>
          {dues === null ? (
            <p>Cargando…</p>
          ) : dues.length === 0 ? (
            <p>Todavía no hay cuotas.</p>
          ) : (
            <ul className="plain-list">
              {dues.map((due) => (
                <li key={due.id}>
                  <div>
                    <strong>{due.name}</strong> — {pesos(due.amountCents)} — vence el {dayMonthYear(due.dueDate)}
                    <br />
                    <span className="muted small">
                      {due.owingCount} de {due.totalCount} deben
                    </span>
                  </div>
                  <div className="panel__actions">
                    <button
                      type="button"
                      className="button button--secondary button--small"
                      disabled={busy}
                      aria-expanded={detail?.id === due.id}
                      aria-label={`Ver detalle de ${due.name}`}
                      onClick={() => void openDetail(due.id)}
                    >
                      Ver detalle
                    </button>
                    {isOfficer && (
                      <>
                        <button
                          type="button"
                          className="button button--secondary button--small"
                          disabled={busy}
                          aria-label={`Editar ${due.name}`}
                          onClick={() => {
                            setShowCreate(false);
                            editForm.clear();
                            setStatus(null);
                            setEditing(due);
                          }}
                        >
                          Editar
                        </button>
                        <button
                          type="button"
                          className="button button--secondary button--small"
                          disabled={busy}
                          aria-label={`Borrar ${due.name}`}
                          onClick={() => setConfirmDelete(due)}
                        >
                          Borrar
                        </button>
                      </>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {canReadFull && detail && (
        <section className="panel">
          <div className="panel__header">
            <div>
              <h2 id="due-detail-title">{detail.name}</h2>
              <p className="muted small">
                {pesos(detail.amountCents)} — vence el {dayMonthYear(detail.dueDate)} · {detail.owingCount} de {detail.totalCount} deben
              </p>
            </div>
            <button type="button" className="button button--ghost button--small" onClick={() => setDetail(null)}>
              Cerrar detalle
            </button>
          </div>
          <div className="table-wrap" tabIndex={0} role="region" aria-labelledby="due-detail-title">
            <table className="data-table">
              <thead>
                <tr>
                  <th scope="col">Alumno</th>
                  <th scope="col">Estado</th>
                </tr>
              </thead>
              <tbody>
                {detail.members.map((member) => (
                  <tr key={member.rosterMemberId}>
                    <th scope="row">{member.fullName}</th>
                    <td data-label="Estado">{memberStatusLabel[member.status] ?? member.status}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {showMine && (
        <section className="panel">
          {canReadFull && <h2>Mis cuotas</h2>}
          {mine === null ? (
            <p>Cargando…</p>
          ) : mine.length === 0 ? (
            <p>Todavía no tenés cuotas asignadas.</p>
          ) : (
            <ul className="plain-list">
              {mine.map((due) => (
                <li key={due.id}>
                  <strong>{due.name}</strong>
                  <br />
                  <span>{myDueText(due)}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {confirmDelete && (
        <ConfirmDialog
          title="Borrar cuota"
          confirmLabel="Confirmar borrado"
          onConfirm={() => void handleDelete(confirmDelete)}
          onCancel={() => setConfirmDelete(null)}
        >
          <p>¿Borrás «{confirmDelete.name}»? Se va a eliminar para todo el padrón.</p>
        </ConfirmDialog>
      )}
    </>
  );
}
