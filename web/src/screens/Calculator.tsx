import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import { createDue } from "../api/dues.ts";
import { errorMessage } from "../api/errors.ts";
import { financeCalculatorSummary, type FinanceCalculatorSummary } from "../api/finance.ts";
import { hasRole, useMe, useSession } from "../auth/session.tsx";
import { Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";
import { Icon } from "../components/Icon.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import {
  calculateDueAmount,
  calculatePricePerPerson,
  calculateRaffle,
  calculateSpendable,
  parsePesosToCents,
} from "../lib/calculator.ts";
import { pesosInput } from "../lib/money.ts";
import { Link } from "../lib/router.tsx";
import { DueForm } from "./Dues.tsx";

type SummaryState =
  | { status: "loading" }
  | { status: "error"; text: string }
  | { status: "ready"; data: FinanceCalculatorSummary };

type CalculatorPanelProps = {
  title: string;
  description: string;
  fieldOrder: readonly string[];
  rawErrors: Record<string, string | undefined>;
  visibleErrors: Record<string, string | undefined>;
  activeField: string | null;
  hasResult: boolean;
  tone?: "ok" | undefined;
  emptyPrompt: string;
  loading?: string | undefined;
  loadError?: { text: string; onRetry: () => void } | undefined;
  onReset: () => void;
  onSubmitInvalid: () => void;
  children: ReactNode;
  resultContent: ReactNode;
};

function useFieldInteraction() {
  const [touched, setTouched] = useState<Record<string, boolean>>({});
  const [submitted, setSubmitted] = useState(false);
  const [activeField, setActiveField] = useState<string | null>(null);

  function touch(name: string) {
    setTouched((prev) => (prev[name] ? prev : { ...prev, [name]: true }));
    setActiveField(name);
  }

  function submit() {
    setSubmitted(true);
    setActiveField(null);
  }

  function reset() {
    setTouched({});
    setSubmitted(false);
    setActiveField(null);
  }

  function isVisible(name: string) {
    return submitted || Boolean(touched[name]);
  }

  return { activeField, touch, submit, reset, isVisible };
}

function CalculatorPanel({
  title,
  description,
  fieldOrder,
  rawErrors,
  visibleErrors,
  activeField,
  hasResult,
  tone,
  emptyPrompt,
  loading,
  loadError,
  onReset,
  onSubmitInvalid,
  children,
  resultContent,
}: CalculatorPanelProps) {
  const titleId = useId();
  const formRef = useRef<HTMLFormElement>(null);

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (hasResult) return;
    onSubmitInvalid();
    const form = formRef.current;
    if (!form) return;
    for (const name of fieldOrder) {
      if (rawErrors[name]) {
        const input = form.elements.namedItem(name);
        if (input instanceof HTMLInputElement) {
          input.focus();
          return;
        }
      }
    }
    const firstInput = form.querySelector<HTMLInputElement>("input");
    firstInput?.focus();
  }

  const activeError =
    (activeField ? visibleErrors[activeField] : undefined) ??
    fieldOrder.map((name) => visibleErrors[name]).find(Boolean);

  return (
    <section className="panel calc-panel" aria-labelledby={titleId}>
      <div className="calc-panel__header">
        <h2 id={titleId}>{title}</h2>
        <p className="muted small">{description}</p>
      </div>

      {loading ? (
        <p className="muted small" aria-busy="true">{loading}</p>
      ) : loadError ? (
        <div className="form-status" data-tone="error" role="alert">
          <p>
            <Icon name="alert" />
            <span>{loadError.text}</span>
          </p>
          <button type="button" className="button button--secondary button--small" onClick={loadError.onRetry}>
            Reintentar
          </button>
        </div>
      ) : (
        <>
          <form ref={formRef} noValidate aria-labelledby={titleId} onSubmit={handleSubmit}>
            {children}
            <div className="calc-actions">
              <button type="button" className="button button--secondary button--small" onClick={onReset}>
                Limpiar
              </button>
            </div>
          </form>

          <div className="calc-result" data-tone={tone} aria-live="polite" aria-atomic="true">
            {hasResult ? resultContent : <p className="muted small">{activeError ?? emptyPrompt}</p>}
          </div>
        </>
      )}
    </section>
  );
}

function PricePerPersonCard() {
  const [totalCost, setTotalCost] = useState("");
  const [peopleCount, setPeopleCount] = useState("");
  const [marginPercent, setMarginPercent] = useState("");
  const fields = useFieldInteraction();

  const outcome = calculatePricePerPerson({ totalCost, peopleCount, marginPercent });
  const outcomeErrors = outcome.ok ? {} : outcome.errors;
  const rawErrors: Record<string, string | undefined> = {
    personTotalCost: outcomeErrors.totalCost,
    peopleCount: outcomeErrors.peopleCount,
    marginPercent: outcomeErrors.marginPercent,
  };
  const visibleErrors: Record<string, string | undefined> = {
    personTotalCost: fields.isVisible("personTotalCost") ? rawErrors.personTotalCost : undefined,
    peopleCount: fields.isVisible("peopleCount") ? rawErrors.peopleCount : undefined,
    marginPercent: fields.isVisible("marginPercent") ? rawErrors.marginPercent : undefined,
  };

  function reset() {
    setTotalCost("");
    setPeopleCount("");
    setMarginPercent("");
    fields.reset();
  }

  return (
    <CalculatorPanel
      title="Precio por persona"
      description="Dividí el costo total entre las personas y sumá un margen opcional. Se redondea hacia arriba a múltiplos de $ 100."
      fieldOrder={["personTotalCost", "peopleCount", "marginPercent"]}
      rawErrors={rawErrors}
      visibleErrors={visibleErrors}
      activeField={fields.activeField}
      hasResult={outcome.ok}
      tone={outcome.ok ? "ok" : undefined}
      emptyPrompt="Completá los datos para calcular el precio por persona."
      onReset={reset}
      onSubmitInvalid={fields.submit}
      resultContent={
        outcome.ok && (
          <>
            <p className="calc-result__label">Precio por persona</p>
            <p className="calc-result__value">{outcome.result.formattedPricePerPerson}</p>
            <ul className="calc-result__details muted">
              <li>Sin redondeo: {outcome.result.formattedExactPerPerson}</li>
              <li>Total con margen: {outcome.result.formattedTotalWithMargin}</li>
            </ul>
          </>
        )
      }
    >
      <Field
        label="Costo total ($)"
        name="personTotalCost"
        type="text"
        inputMode="decimal"
        autoComplete="off"
        enterKeyHint="next"
        hint="Ej.: 180000 o 180.000"
        value={totalCost}
        error={visibleErrors.personTotalCost}
        onBlur={() => fields.touch("personTotalCost")}
        onChange={(event) => {
          fields.touch("personTotalCost");
          setTotalCost(event.target.value);
        }}
      />
      <Field
        label="Cantidad de personas"
        name="peopleCount"
        type="text"
        inputMode="numeric"
        autoComplete="off"
        enterKeyHint="next"
        value={peopleCount}
        error={visibleErrors.peopleCount}
        onBlur={() => fields.touch("peopleCount")}
        onChange={(event) => {
          fields.touch("peopleCount");
          setPeopleCount(event.target.value);
        }}
      />
      <Field
        label="Margen opcional (%)"
        name="marginPercent"
        type="text"
        inputMode="decimal"
        autoComplete="off"
        enterKeyHint="done"
        hint="Dejalo vacío si no querés sumar margen."
        value={marginPercent}
        error={visibleErrors.marginPercent}
        onBlur={() => fields.touch("marginPercent")}
        onChange={(event) => {
          fields.touch("marginPercent");
          setMarginPercent(event.target.value);
        }}
      />
    </CalculatorPanel>
  );
}

function RaffleCard() {
  const [totalCost, setTotalCost] = useState("");
  const [unitPrice, setUnitPrice] = useState("");
  const [availableUnits, setAvailableUnits] = useState("");
  const fields = useFieldInteraction();

  const outcome = calculateRaffle({ totalCost, unitPrice, availableUnits });
  const outcomeErrors = outcome.ok ? {} : outcome.errors;
  const rawErrors: Record<string, string | undefined> = {
    raffleTotalCost: outcomeErrors.totalCost,
    unitPrice: outcomeErrors.unitPrice,
    availableUnits: outcomeErrors.availableUnits,
  };
  const visibleErrors: Record<string, string | undefined> = {
    raffleTotalCost: fields.isVisible("raffleTotalCost") ? rawErrors.raffleTotalCost : undefined,
    unitPrice: fields.isVisible("unitPrice") ? rawErrors.unitPrice : undefined,
    availableUnits: fields.isVisible("availableUnits") ? rawErrors.availableUnits : undefined,
  };

  function reset() {
    setTotalCost("");
    setUnitPrice("");
    setAvailableUnits("");
    fields.reset();
  }

  return (
    <CalculatorPanel
      title="Rifa o venta"
      description="Calculá cuántas unidades tenés que vender para no perder y cuánto ganás si vendés todo."
      fieldOrder={["raffleTotalCost", "unitPrice", "availableUnits"]}
      rawErrors={rawErrors}
      visibleErrors={visibleErrors}
      activeField={fields.activeField}
      hasResult={outcome.ok}
      tone={outcome.ok && outcome.result.coversCost ? "ok" : undefined}
      emptyPrompt="Completá los datos para calcular la rifa o venta."
      onReset={reset}
      onSubmitInvalid={fields.submit}
      resultContent={
        outcome.ok && (
          <>
            <p className="calc-result__label">Para no perder</p>
            <p className="calc-result__value">
              {outcome.result.breakEvenUnits} {outcome.result.breakEvenUnits === 1 ? "unidad" : "unidades"}
            </p>
            {outcome.result.coversCost ? (
              <ul className="calc-result__details">
                <li>
                  Ganancia si se vende todo: <strong>{outcome.result.formattedProfitOrLoss}</strong>
                </li>
                <li className="muted">Recaudación máxima: {outcome.result.formattedMaxRevenue}</li>
              </ul>
            ) : (
              <>
                <div className="form-status" data-tone="error">
                  <p>
                    <Icon name="alert" />
                    <span>{outcome.result.lossMessage}</span>
                  </p>
                </div>
                <p className="muted small">Recaudación máxima: {outcome.result.formattedMaxRevenue}</p>
              </>
            )}
          </>
        )
      }
    >
      <Field
        label="Costo total de premios o mercadería ($)"
        name="raffleTotalCost"
        type="text"
        inputMode="decimal"
        autoComplete="off"
        enterKeyHint="next"
        hint="Ej.: 60000"
        value={totalCost}
        error={visibleErrors.raffleTotalCost}
        onBlur={() => fields.touch("raffleTotalCost")}
        onChange={(event) => {
          fields.touch("raffleTotalCost");
          setTotalCost(event.target.value);
        }}
      />
      <Field
        label="Precio por unidad ($)"
        name="unitPrice"
        type="text"
        inputMode="decimal"
        autoComplete="off"
        enterKeyHint="next"
        value={unitPrice}
        error={visibleErrors.unitPrice}
        onBlur={() => fields.touch("unitPrice")}
        onChange={(event) => {
          fields.touch("unitPrice");
          setUnitPrice(event.target.value);
        }}
      />
      <Field
        label="Unidades disponibles"
        name="availableUnits"
        type="text"
        inputMode="numeric"
        autoComplete="off"
        enterKeyHint="done"
        value={availableUnits}
        error={visibleErrors.availableUnits}
        onBlur={() => fields.touch("availableUnits")}
        onChange={(event) => {
          fields.touch("availableUnits");
          setAvailableUnits(event.target.value);
        }}
      />
    </CalculatorPanel>
  );
}

type DueCreateDialogProps = {
  amountCents: number;
  onCreated: () => void;
  onCancel: () => void;
};

/** Native modal <dialog> (Modern Web Guidance `dialog`) reusing #74's DueForm with a prefilled amount. */
function DueCreateDialog({ amountCents, onCreated, onCancel }: DueCreateDialogProps) {
  const { refresh } = useSession();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const createForm = useFormErrors();
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);

  useLayoutEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
    dialog.querySelector<HTMLInputElement>('input[name="name"]')?.focus();
    return () => {
      if (dialog.open && typeof dialog.close === "function") dialog.close();
    };
  }, []);

  async function handleSubmit(values: { name: string; amountCents: string; dueDate: string }) {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setStatus(null);
    const result = await createDue(values);
    if (result.ok) {
      pending.current = false;
      setBusy(false);
      onCreated();
      return;
    }
    if (result.code === "mfa_required" || result.code === "unauthenticated") void refresh();
    setStatus({ tone: "error", text: errorMessage(result.code) });
    createForm.failed(result.code);
    pending.current = false;
    setBusy(false);
  }

  return (
    <dialog
      ref={dialogRef}
      className="confirm"
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onCancel();
      }}
    >
      <FormStatus status={status} {...createForm.statusProps} />
      <DueForm
        plain
        headingId={titleId}
        heading="Nueva cuota"
        submitLabel="Guardar"
        busy={busy}
        initial={{ name: "", amount: pesosInput(String(amountCents)), dueDate: "" }}
        form={createForm}
        onSubmit={(values) => void handleSubmit(values)}
        onCancel={() => {
          if (!busy) onCancel();
        }}
      />
    </dialog>
  );
}

function DueAmountCard({
  summaryState,
  onRetry,
  onDueCreated,
}: {
  summaryState: SummaryState;
  onRetry: () => void;
  onDueCreated: () => void;
}) {
  const [goal, setGoal] = useState("");
  const [expectedExempt, setExpectedExempt] = useState("0");
  const [dialogAmountCents, setDialogAmountCents] = useState<number | null>(null);
  const [createdStatus, setCreatedStatus] = useState<Status>(null);
  const createdStatusRef = useRef<HTMLDivElement>(null);
  const openButtonRef = useRef<HTMLButtonElement>(null);
  const focusNext = useRef<"status" | "button" | null>(null);
  const fields = useFieldInteraction();

  useEffect(() => {
    if (!focusNext.current) return;
    const target = focusNext.current;
    focusNext.current = null;
    if (target === "status") createdStatusRef.current?.focus();
    else openButtonRef.current?.focus();
  });

  const ready = summaryState.status === "ready";
  const rosterCount = ready ? summaryState.data.rosterCount : 0;
  const outcome = ready ? calculateDueAmount({ goal, rosterCount, expectedExempt }) : null;
  const rawErrors: Record<string, string | undefined> = {};
  if (outcome && !outcome.ok) {
    const parsedGoal = parsePesosToCents(goal);
    const goalInvalid = !parsedGoal.ok || parsedGoal.cents <= 0 || rosterCount <= 0;
    const trimmedExempt = expectedExempt.trim();
    const exemptBadFormat = trimmedExempt !== "" && !/^\d+$/.test(trimmedExempt);
    const exemptCount = trimmedExempt === "" ? 0 : Number(trimmedExempt);
    const exemptInvalid = exemptBadFormat || rosterCount <= 0 || exemptCount >= rosterCount;
    if (goalInvalid) rawErrors.dueGoal = outcome.error;
    if (exemptInvalid) rawErrors.expectedExempt = outcome.error;
    if (!goalInvalid && !exemptInvalid) rawErrors.dueGoal = outcome.error;
  }
  const visibleErrors: Record<string, string | undefined> = {
    dueGoal: fields.isVisible("dueGoal") ? rawErrors.dueGoal : undefined,
    expectedExempt: fields.isVisible("expectedExempt") ? rawErrors.expectedExempt : undefined,
  };

  function reset() {
    setGoal("");
    setExpectedExempt("0");
    setCreatedStatus(null);
    fields.reset();
  }

  return (
    <>
      <CalculatorPanel
        title="Monto de cuota"
        description={
          ready
            ? `Dividí la meta de recaudación entre los alumnos del padrón que van a pagar. Padrón actual: ${rosterCount} ${rosterCount === 1 ? "alumno" : "alumnos"}.`
            : "Dividí la meta de recaudación entre los alumnos del padrón que van a pagar. Se redondea hacia arriba a múltiplos de $ 100."
        }
        fieldOrder={["dueGoal", "expectedExempt"]}
        rawErrors={rawErrors}
        visibleErrors={visibleErrors}
        activeField={fields.activeField}
        hasResult={Boolean(outcome?.ok)}
        tone={outcome?.ok ? "ok" : undefined}
        emptyPrompt="Ingresá la meta de recaudación para calcular el monto por alumno."
        loading={summaryState.status === "loading" ? "Cargando datos del padrón…" : undefined}
        loadError={summaryState.status === "error" ? { text: summaryState.text, onRetry } : undefined}
        onReset={reset}
        onSubmitInvalid={fields.submit}
        resultContent={
          outcome?.ok && (
            <>
              <p className="calc-result__label">Monto por alumno</p>
              <p className="calc-result__value">{outcome.result.formattedAmountPerStudent}</p>
              <ul className="calc-result__details muted">
                <li>Sin redondeo: {outcome.result.formattedExactPerStudent}</li>
                <li>
                  Alumnos que pagan: {outcome.result.payingCount} de {rosterCount}
                </li>
              </ul>
              <div className="calc-actions">
                <button
                  ref={openButtonRef}
                  type="button"
                  className="button button--primary"
                  onClick={() => {
                    setCreatedStatus(null);
                    setDialogAmountCents(outcome.result.amountPerStudentCents);
                  }}
                >
                  Crear Cuota con este monto
                </button>
              </div>
            </>
          )
        }
      >
        <FormStatus status={createdStatus} ref={createdStatusRef} />
        <Field
          label="Meta de recaudación ($)"
          name="dueGoal"
          type="text"
          inputMode="decimal"
          autoComplete="off"
          enterKeyHint="next"
          hint="Ej.: 600000 o 600.000"
          value={goal}
          error={visibleErrors.dueGoal}
          onBlur={() => fields.touch("dueGoal")}
          onChange={(event) => {
            fields.touch("dueGoal");
            setGoal(event.target.value);
          }}
        />
        <Field
          label="Exentos previstos"
          name="expectedExempt"
          type="text"
          inputMode="numeric"
          autoComplete="off"
          enterKeyHint="done"
          hint="Alumnos que estimás eximir en esta cuota (por defecto 0)."
          value={expectedExempt}
          error={visibleErrors.expectedExempt}
          onBlur={() => fields.touch("expectedExempt")}
          onChange={(event) => {
            fields.touch("expectedExempt");
            setExpectedExempt(event.target.value);
          }}
        />
      </CalculatorPanel>

      {dialogAmountCents !== null && (
        <DueCreateDialog
          amountCents={dialogAmountCents}
          onCancel={() => {
            focusNext.current = "button";
            setDialogAmountCents(null);
          }}
          onCreated={() => {
            focusNext.current = "status";
            setDialogAmountCents(null);
            setCreatedStatus({ tone: "ok", text: "Se creó la cuota." });
            onDueCreated();
          }}
        />
      )}
    </>
  );
}

function SpendableCard({ summaryState, onRetry }: { summaryState: SummaryState; onRetry: () => void }) {
  const [plannedCost, setPlannedCost] = useState("");
  const fields = useFieldInteraction();

  const ready = summaryState.status === "ready";
  const spendable = ready
    ? calculateSpendable({
        balanceCents: summaryState.data.balance,
        owedOnDuesCents: summaryState.data.owedOnDues,
        plannedCost,
      })
    : null;
  const rawErrors: Record<string, string | undefined> = {
    plannedCost: spendable?.plannedCostError,
  };
  const visibleErrors: Record<string, string | undefined> = {
    plannedCost: fields.isVisible("plannedCost") ? rawErrors.plannedCost : undefined,
  };
  const hasResult = Boolean(spendable && !spendable.plannedCostError);

  function reset() {
    setPlannedCost("");
    fields.reset();
  }

  return (
    <CalculatorPanel
      title="Cuánto podemos gastar"
      description="Suma el saldo actual de la caja y lo que todavía se debe en cuotas. Podés comparar contra un costo planeado."
      fieldOrder={["plannedCost"]}
      rawErrors={rawErrors}
      visibleErrors={visibleErrors}
      activeField={fields.activeField}
      hasResult={hasResult}
      tone={hasResult && spendable?.comparison?.status === "covered_today" ? "ok" : undefined}
      emptyPrompt="Ingresá un costo planeado válido para comparar contra lo disponible."
      loading={summaryState.status === "loading" ? "Cargando saldo y cuotas pendientes…" : undefined}
      loadError={summaryState.status === "error" ? { text: summaryState.text, onRetry } : undefined}
      onReset={reset}
      onSubmitInvalid={fields.submit}
      resultContent={
        spendable && (
          <>
            <p className="calc-result__label">Disponible</p>
            <p className="calc-result__value">{spendable.summaryMessage}</p>
            <ul className="calc-result__details muted">
              <li>Saldo actual de la caja: {spendable.formattedBalance}</li>
              <li>Pendiente de cuotas: {spendable.formattedOwedOnDues}</li>
            </ul>
            {spendable.comparison && (
              <div
                className="form-status"
                data-tone={
                  spendable.comparison.status === "short"
                    ? "error"
                    : spendable.comparison.status === "covered_today"
                      ? "ok"
                      : "info"
                }
              >
                <p>
                  <Icon name={spendable.comparison.status === "short" ? "alert" : "check"} />
                  <span>{spendable.comparison.message}</span>
                </p>
              </div>
            )}
          </>
        )
      }
    >
      <Field
        label="Costo planeado opcional ($)"
        name="plannedCost"
        type="text"
        inputMode="decimal"
        autoComplete="off"
        enterKeyHint="done"
        hint="Dejalo vacío para ver solo cuánto tenés disponible."
        value={plannedCost}
        error={visibleErrors.plannedCost}
        onBlur={() => fields.touch("plannedCost")}
        onChange={(event) => {
          fields.touch("plannedCost");
          setPlannedCost(event.target.value);
        }}
      />
    </CalculatorPanel>
  );
}

/** Officer-only quick calculators; calculations live in memory only and never write to the API unless a Due is confirmed. */
export function Calculator() {
  useDocumentTitle("Calculadora");
  const me = useMe();
  const { refresh } = useSession();
  const isOfficer = hasRole(me, "officer");
  const [summaryState, setSummaryState] = useState<SummaryState>({ status: "loading" });

  const loadSummary = useCallback(async (background = false) => {
    if (!background) setSummaryState({ status: "loading" });
    const result = await financeCalculatorSummary();
    if (result.ok) {
      setSummaryState({ status: "ready", data: result.data });
      return;
    }
    if (result.code === "mfa_required" || result.code === "unauthenticated") void refresh();
    if (!background) setSummaryState({ status: "error", text: errorMessage(result.code) });
  }, [refresh]);

  useEffect(() => {
    if (!isOfficer) return;
    void loadSummary();
  }, [isOfficer, loadSummary]);

  if (!isOfficer) {
    return (
      <>
        <header className="page-header">
          <PageTitle eyebrow="Comisión directiva">Calculadora</PageTitle>
        </header>
        <section className="banner" role="alert" aria-labelledby="no-access-title">
          <Icon name="alert" />
          <div className="banner__body">
            <h2 id="no-access-title">No tenés acceso a esta sección</h2>
            <p>Solo quienes integran la comisión directiva pueden usar la calculadora.</p>
          </div>
          <Link className="button button--secondary" to="/">
            Volver al inicio
          </Link>
        </section>
      </>
    );
  }

  return (
    <>
      <header className="page-header">
        <div>
          <PageTitle eyebrow="Comisión directiva">Calculadora</PageTitle>
          <p className="page-header__school">
            Cálculos rápidos para organizar actividades y ventas. Los datos no se guardan ni tocan la caja.
          </p>
        </div>
      </header>
      <p><Link to="/presupuestos">Presupuestos de evento</Link></p>
      <div className="calc-grid">
        <PricePerPersonCard />
        <DueAmountCard
          summaryState={summaryState}
          onRetry={() => void loadSummary()}
          onDueCreated={() => void loadSummary(true)}
        />
        <RaffleCard />
        <SpendableCard summaryState={summaryState} onRetry={() => void loadSummary()} />
      </div>
    </>
  );
}
