import { useCallback, useEffect, useId, useRef, useState } from "react";
import {
  createDue,
  deleteDue,
  dueMembersCsvHref,
  exemptDueMember,
  getDue,
  listDues,
  listMyDues,
  recordDuePayment,
  updateDue,
  type DueDetail,
  type DueMember,
  type DueSummary,
  type MyDue,
} from "../api/dues.ts";
import { errorMessage, type ErrorContext } from "../api/errors.ts";
import { canReadDues, hasRole, useMe, useSession } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { dayMonthYear, formatPesos, parsePesos, pesosInput, todayArgentina } from "../lib/money.ts";

const memberStatusLabel: Record<string, string> = {
  owing: "Debe",
  paid: "Pagó",
  exempt: "Exento",
};

/** What a member still owes of a due, in centavos. */
const owedCents = (amountCents: string, paidCents: string) => String(BigInt(amountCents) - BigInt(paidCents));

/** "Pagó $ 500,00 de $ 2.000,00" while a payment is partial; otherwise the status label. */
function memberStatusText(member: DueMember, amountCents: string): string {
  if (member.status === "owing" && member.paidCents !== "0") return `Pagó ${formatPesos(member.paidCents)} de ${formatPesos(amountCents)}`;
  return memberStatusLabel[member.status] ?? member.status;
}

function myDueText(due: MyDue): string {
  const formatted = formatPesos(due.amountCents);
  const formattedDate = dayMonthYear(due.dueDate);
  if (due.status === "paid") return `Pagada ${formatted} — vencía el ${formattedDate}`;
  // S6: the exempt student sees only "Exento", never the reason.
  if (due.status === "exempt") return "Exento";
  if (due.paidCents !== "0") {
    return `Pagaste ${formatPesos(due.paidCents)} de ${formatted}, te falta ${formatPesos(owedCents(due.amountCents, due.paidCents))} — vence el ${formattedDate}`;
  }
  return `Debés ${formatted} — vence el ${formattedDate}`;
}

type PaymentFormProps = {
  member: DueMember;
  owed: string;
  busy: boolean;
  form: ReturnType<typeof useFormErrors>;
  onSubmit: (values: { amountCents: string; date: string }) => void;
  onCancel: () => void;
};

function PaymentForm({ member, owed, busy, form, onSubmit, onCancel }: PaymentFormProps) {
  const headingId = useId();
  const [amount, setAmount] = useState(pesosInput(owed));
  const [date, setDate] = useState(todayArgentina());

  function handleSubmit() {
    if (!form.validate({
      amount: (v) => (parsePesos(v) === null ? "Ingresá un monto positivo con hasta dos decimales." : null),
      date: (v) => (!v ? "Elegí la fecha del pago." : null),
    })) {
      return;
    }
    onSubmit({ amountCents: String(parsePesos(amount)!), date });
  }

  return (
    <section className="panel">
      <h2 id={headingId}>Registrar pago de {member.fullName}</h2>
      <p className="muted small">Falta pagar {formatPesos(owed)}.</p>
      <form
        aria-labelledby={headingId}
        {...form.formProps}
        onSubmit={(event) => {
          event.preventDefault();
          handleSubmit();
        }}
      >
        <Field label="Monto en pesos" inputMode="decimal" {...form.field("amount")} value={amount} onChange={(e) => setAmount(e.target.value)} />
        <Field label="Fecha del pago" type="date" {...form.field("date")} value={date} onChange={(e) => setDate(e.target.value)} />
        <div className="panel__actions">
          <button type="submit" className="button button--primary" disabled={busy}>
            Guardar pago
          </button>
          <button type="button" className="button button--secondary" disabled={busy} onClick={onCancel}>
            Cancelar
          </button>
        </div>
      </form>
    </section>
  );
}

export type DueFormProps = {
  heading: string;
  headingId?: string | undefined;
  plain?: boolean | undefined;
  submitLabel: string;
  busy: boolean;
  initial?: { name: string; amount: string; dueDate: string };
  form: ReturnType<typeof useFormErrors>;
  onSubmit: (values: { name: string; amountCents: string; dueDate: string }) => void;
  onCancel: () => void;
};

export function DueForm({ heading, headingId: providedHeadingId, plain, submitLabel, busy, initial, form, onSubmit, onCancel }: DueFormProps) {
  const generatedHeadingId = useId();
  const headingId = providedHeadingId ?? generatedHeadingId;
  const [name, setName] = useState(initial?.name ?? "");
  const [amount, setAmount] = useState(initial?.amount ?? "");
  const [dueDate, setDueDate] = useState(initial?.dueDate ?? "");

  function handleSubmit() {
    if (!form.validate({
      name: (v) => (!v.trim() ? "Escribí un nombre." : null),
      amount: (v) => (parsePesos(v) === null ? "Ingresá un monto positivo con hasta dos decimales." : null),
      dueDate: (v) => (!v ? "Elegí una fecha de vencimiento." : null),
    })) {
      return;
    }
    onSubmit({ name: name.trim(), amountCents: String(parsePesos(amount)!), dueDate });
  }

  const content = (
    <>
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
    </>
  );

  return plain ? content : <section className="panel">{content}</section>;
}

type ExemptTarget = { rosterMemberId: string; fullName: string };

const REASON_MAX = 500;

type ExemptFormProps = {
  member: ExemptTarget;
  busy: boolean;
  form: ReturnType<typeof useFormErrors>;
  onSubmit: (reason: string) => void;
  onCancel: () => void;
};

function ExemptForm({ member, busy, form, onSubmit, onCancel }: ExemptFormProps) {
  const headingId = useId();
  const [reason, setReason] = useState("");

  function handleSubmit() {
    if (!form.validate({
      reason: (v) => {
        const trimmed = v.trim();
        if (!trimmed) return errorMessage("reason_required", "due-exemption");
        // The API counts characters after trimming, not UTF-16 units.
        return [...trimmed].length > REASON_MAX ? errorMessage("reason_too_long") : null;
      },
    })) {
      return;
    }
    onSubmit(reason.trim());
  }

  return (
    <form
      aria-labelledby={headingId}
      {...form.formProps}
      onSubmit={(event) => {
        event.preventDefault();
        handleSubmit();
      }}
    >
      <h3 id={headingId}>Eximir a {member.fullName}</h3>
      <Field
        label="Motivo"
        hint="Hasta 500 caracteres. El alumno solo ve «Exento», no el motivo. La exención no se puede deshacer."
        autoFocus
        {...form.field("reason")}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
      />
      <div className="panel__actions">
        <button type="submit" className="button button--primary" disabled={busy}>
          Confirmar exención
        </button>
        <button type="button" className="button button--secondary" disabled={busy} onClick={onCancel}>
          Cancelar
        </button>
      </div>
    </form>
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
  const [paying, setPaying] = useState<DueMember | null>(null);
  const [exempting, setExempting] = useState<ExemptTarget | null>(null);
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  const createForm = useFormErrors();
  const editForm = useFormErrors();
  const paymentForm = useFormErrors();
  const exemptForm = useFormErrors();
  // After a successful create, edit, delete, payment or exemption the focused form or dialog
  // unmounts: move focus to the success message so it never stays on body (CS-9).
  const focusStatus = useRef(false);

  const failed = useCallback(
    (code: string, targetForm?: ReturnType<typeof useFormErrors>, context?: ErrorContext) => {
      if (code === "mfa_required" || code === "unauthenticated") void refresh();
      setStatus({ tone: "error", text: errorMessage(code, context) });
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
      focusStatus.current = true;
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
      focusStatus.current = true;
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
      focusStatus.current = true;
      if (detail?.id === target.id) {
        setDetail(null);
        setExempting(null);
      }
      if (editing?.id === target.id) setEditing(null);
      setStatus({ tone: "ok", text: "Se borró la cuota." });
      await load();
    } else {
      // The dialog that held focus is gone and there is no form to mark: focus the message (CS-9).
      focusStatus.current = true;
      failed(result.code);
    }
    pending.current = false;
    setBusy(false);
  }

  async function openDetail(id: string) {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setPaying(null);
    const result = await getDue(id);
    if (result.ok) {
      setDetail(result.data);
      setExempting(null);
    } else {
      failed(result.code);
    }
    pending.current = false;
    setBusy(false);
  }

  async function handlePayment(values: { amountCents: string; date: string }) {
    if (!detail || !paying || pending.current) return;
    pending.current = true;
    setBusy(true);
    setStatus(null);
    const result = await recordDuePayment(detail.id, { rosterMemberId: paying.rosterMemberId, ...values });
    if (result.ok) {
      focusStatus.current = true;
      setPaying(null);
      setStatus({ tone: "ok", text: "Se registró el pago." });
      const refreshed = await getDue(detail.id);
      if (refreshed.ok) setDetail(refreshed.data);
      await load();
    } else {
      failed(result.code, paymentForm);
    }
    pending.current = false;
    setBusy(false);
  }

  async function handleExempt(reason: string) {
    if (!detail || !exempting || pending.current) return;
    pending.current = true;
    setBusy(true);
    setStatus(null);
    const result = await exemptDueMember(detail.id, { rosterMemberId: exempting.rosterMemberId, reason });
    if (result.ok) {
      focusStatus.current = true;
      setExempting(null);
      setDetail(result.data);
      setStatus({ tone: "ok", text: "Se registró la exención." });
      await load();
    } else {
      // Messages are fixed text per code: never the reason or the student's name (S9).
      failed(result.code, exemptForm, "due-exemption");
    }
    pending.current = false;
    setBusy(false);
  }

  const activeForm = exempting ? exemptForm : editing ? editForm : paying ? paymentForm : createForm;

  useEffect(() => {
    if (!focusStatus.current) return;
    focusStatus.current = false;
    activeForm.statusProps.ref.current?.focus();
  });

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
                setPaying(null);
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
                    <strong>{due.name}</strong> — {formatPesos(due.amountCents)} — vence el {dayMonthYear(due.dueDate)}
                    <br />
                    <span className="muted small">
                      {due.totalCount - due.owingCount} de {due.totalCount} pagaron
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
                            setPaying(null);
                            setExempting(null);
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
                {formatPesos(detail.amountCents)} — vence el {dayMonthYear(detail.dueDate)} ·{" "}
                {detail.totalCount - detail.owingCount} de {detail.totalCount} pagaron
              </p>
            </div>
            <div className="panel__actions">
              <a className="button button--secondary button--small" href={dueMembersCsvHref(detail.id)} download>
                Descargar CSV
              </a>
              <button
                type="button"
                className="button button--ghost button--small"
                onClick={() => {
                  setPaying(null);
                  setExempting(null);
                  setDetail(null);
                }}
              >
                Cerrar detalle
              </button>
            </div>
          </div>
          <div className="table-wrap" tabIndex={0} role="region" aria-labelledby="due-detail-title">
            <table className="data-table">
              <thead>
                <tr>
                  <th scope="col">Alumno</th>
                  <th scope="col">Estado</th>
                  {isOfficer && <th scope="col">Acciones</th>}
                </tr>
              </thead>
              <tbody>
                {detail.members.map((member) => (
                  <tr key={member.rosterMemberId}>
                    <th scope="row">{member.fullName}</th>
                    <td data-label="Estado">
                      <span>{memberStatusText(member, detail.amountCents)}</span>
                      {member.status === "exempt" && member.exemptionReason && (
                        <>
                          <br />
                          <span className="muted small">Motivo: {member.exemptionReason}</span>
                        </>
                      )}
                    </td>
                    {isOfficer && (
                      <td data-label="Acciones">
                        {member.status === "owing" && (
                          <button
                            type="button"
                            className="button button--secondary button--small"
                            disabled={busy}
                            aria-label={`Registrar pago de ${member.fullName}`}
                            onClick={() => {
                              setShowCreate(false);
                              setEditing(null);
                              setExempting(null);
                              paymentForm.clear();
                              setStatus(null);
                              setPaying(member);
                            }}
                          >
                            Registrar pago
                          </button>
                        )}{" "}
                        {/* Members with payments are left out: the API answers has_payments (#79 R3). */}
                        {member.status === "owing" && member.paidCents === "0" && (
                          <button
                            type="button"
                            className="button button--secondary button--small"
                            disabled={busy}
                            aria-label={`Eximir a ${member.fullName}`}
                            onClick={() => {
                              setShowCreate(false);
                              setEditing(null);
                              setPaying(null);
                              exemptForm.clear();
                              setStatus(null);
                              setExempting({ rosterMemberId: member.rosterMemberId, fullName: member.fullName });
                            }}
                          >
                            Eximir
                          </button>
                        )}
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {isOfficer && exempting && (
            <ExemptForm
              key={exempting.rosterMemberId}
              member={exempting}
              busy={busy}
              form={exemptForm}
              onSubmit={(reason) => void handleExempt(reason)}
              onCancel={() => {
                setExempting(null);
                exemptForm.clear();
              }}
            />
          )}
        </section>
      )}

      {isOfficer && detail && paying && (
        <PaymentForm
          key={paying.rosterMemberId}
          member={paying}
          owed={owedCents(detail.amountCents, paying.paidCents)}
          busy={busy}
          form={paymentForm}
          onSubmit={(values) => void handlePayment(values)}
          onCancel={() => {
            setPaying(null);
            paymentForm.clear();
          }}
        />
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
