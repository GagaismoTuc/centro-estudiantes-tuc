import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { hasRole, useMe } from "../auth/session.tsx";
import { Field } from "../components/Form.tsx";
import { Icon } from "../components/Icon.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { calculatePricePerPerson, calculateRaffle } from "../lib/calculator.ts";
import { Link } from "../lib/router.tsx";

type CalculatorPanelProps = {
  title: string;
  description: string;
  fieldOrder: readonly string[];
  errors: Record<string, string | undefined>;
  hasResult: boolean;
  tone?: "ok" | undefined;
  interacted: boolean;
  emptyPrompt: string;
  onReset: () => void;
  onSubmitInvalid: () => void;
  children: ReactNode;
  resultContent: ReactNode;
};

function CalculatorPanel({
  title,
  description,
  fieldOrder,
  errors,
  hasResult,
  tone,
  interacted,
  emptyPrompt,
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
      if (errors[name]) {
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

  const activeError = fieldOrder.map((name) => errors[name]).find(Boolean);

  return (
    <section className="panel calc-panel" aria-labelledby={titleId}>
      <div className="calc-panel__header">
        <h2 id={titleId}>{title}</h2>
        <p className="muted small">{description}</p>
      </div>

      <form ref={formRef} noValidate aria-labelledby={titleId} onSubmit={handleSubmit}>
        {children}
        <div className="calc-actions">
          <button type="button" className="button button--secondary button--small" onClick={onReset}>
            Limpiar
          </button>
        </div>
      </form>

      <div className="calc-result" data-tone={tone} aria-live="polite" aria-atomic="true">
        {hasResult ? (
          resultContent
        ) : (
          <p className="muted small">{interacted ? (activeError ?? "Revisá los datos para ver el resultado.") : emptyPrompt}</p>
        )}
      </div>
    </section>
  );
}

function PricePerPersonCard() {
  const [totalCost, setTotalCost] = useState("");
  const [peopleCount, setPeopleCount] = useState("");
  const [marginPercent, setMarginPercent] = useState("");
  const [touched, setTouched] = useState<Record<string, boolean>>({});
  const [submitted, setSubmitted] = useState(false);

  const outcome = calculatePricePerPerson({ totalCost, peopleCount, marginPercent });
  const rawErrors = outcome.ok ? {} : outcome.errors;
  const interacted = submitted || Object.values(touched).some(Boolean);
  const visibleErrors: Record<string, string | undefined> = {
    personTotalCost: submitted || touched.personTotalCost ? rawErrors.totalCost : undefined,
    peopleCount: submitted || touched.peopleCount ? rawErrors.peopleCount : undefined,
    marginPercent: submitted || touched.marginPercent ? rawErrors.marginPercent : undefined,
  };

  function mark(name: string) {
    setTouched((prev) => (prev[name] ? prev : { ...prev, [name]: true }));
  }

  function reset() {
    setTotalCost("");
    setPeopleCount("");
    setMarginPercent("");
    setTouched({});
    setSubmitted(false);
  }

  return (
    <CalculatorPanel
      title="Precio por persona"
      description="Dividí el costo total entre las personas y sumá un margen opcional. Se redondea hacia arriba a múltiplos de $ 100."
      fieldOrder={["personTotalCost", "peopleCount", "marginPercent"]}
      errors={{
        personTotalCost: rawErrors.totalCost,
        peopleCount: rawErrors.peopleCount,
        marginPercent: rawErrors.marginPercent,
      }}
      hasResult={outcome.ok}
      tone={outcome.ok ? "ok" : undefined}
      interacted={interacted}
      emptyPrompt="Completá los datos para calcular el precio por persona."
      onReset={reset}
      onSubmitInvalid={() => setSubmitted(true)}
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
        onBlur={() => mark("personTotalCost")}
        onChange={(event) => {
          mark("personTotalCost");
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
        onBlur={() => mark("peopleCount")}
        onChange={(event) => {
          mark("peopleCount");
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
        onBlur={() => mark("marginPercent")}
        onChange={(event) => {
          mark("marginPercent");
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
  const [touched, setTouched] = useState<Record<string, boolean>>({});
  const [submitted, setSubmitted] = useState(false);

  const outcome = calculateRaffle({ totalCost, unitPrice, availableUnits });
  const rawErrors = outcome.ok ? {} : outcome.errors;
  const interacted = submitted || Object.values(touched).some(Boolean);
  const visibleErrors: Record<string, string | undefined> = {
    raffleTotalCost: submitted || touched.raffleTotalCost ? rawErrors.totalCost : undefined,
    unitPrice: submitted || touched.unitPrice ? rawErrors.unitPrice : undefined,
    availableUnits: submitted || touched.availableUnits ? rawErrors.availableUnits : undefined,
  };

  function mark(name: string) {
    setTouched((prev) => (prev[name] ? prev : { ...prev, [name]: true }));
  }

  function reset() {
    setTotalCost("");
    setUnitPrice("");
    setAvailableUnits("");
    setTouched({});
    setSubmitted(false);
  }

  return (
    <CalculatorPanel
      title="Rifa o venta"
      description="Calculá cuántas unidades tenés que vender para no perder y cuánto ganás si vendés todo."
      fieldOrder={["raffleTotalCost", "unitPrice", "availableUnits"]}
      errors={{
        raffleTotalCost: rawErrors.totalCost,
        unitPrice: rawErrors.unitPrice,
        availableUnits: rawErrors.availableUnits,
      }}
      hasResult={outcome.ok}
      tone={outcome.ok && outcome.result.coversCost ? "ok" : undefined}
      interacted={interacted}
      emptyPrompt="Completá los datos para calcular la rifa o venta."
      onReset={reset}
      onSubmitInvalid={() => setSubmitted(true)}
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
        onBlur={() => mark("raffleTotalCost")}
        onChange={(event) => {
          mark("raffleTotalCost");
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
        onBlur={() => mark("unitPrice")}
        onChange={(event) => {
          mark("unitPrice");
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
        onBlur={() => mark("availableUnits")}
        onChange={(event) => {
          mark("availableUnits");
          setAvailableUnits(event.target.value);
        }}
      />
    </CalculatorPanel>
  );
}

/** Officer-only quick calculators; state lives in memory only and never writes to the API. */
export function Calculator() {
  useDocumentTitle("Calculadora");
  const me = useMe();

  if (!hasRole(me, "officer")) {
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
      <div className="calc-grid">
        <PricePerPersonCard />
        <RaffleCard />
      </div>
    </>
  );
}
