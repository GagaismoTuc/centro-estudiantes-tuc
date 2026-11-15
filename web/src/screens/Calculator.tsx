import { useState } from "react";
import { hasRole, useMe } from "../auth/session.tsx";
import { Field } from "../components/Form.tsx";
import { Icon } from "../components/Icon.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";
import { calculatePricePerPerson, calculateRaffle } from "../lib/calculator.ts";
import { Link } from "../lib/router.tsx";

function PricePerPersonCard() {
  const [totalCost, setTotalCost] = useState("");
  const [peopleCount, setPeopleCount] = useState("");
  const [marginPercent, setMarginPercent] = useState("");
  const [interacted, setInteracted] = useState(false);

  const outcome = calculatePricePerPerson({ totalCost, peopleCount, marginPercent });
  const errors = interacted && !outcome.ok ? outcome.errors : {};

  function reset() {
    setTotalCost("");
    setPeopleCount("");
    setMarginPercent("");
    setInteracted(false);
  }

  return (
    <section className="panel calc-panel" aria-labelledby="calc-person-title">
      <div className="calc-panel__header">
        <h2 id="calc-person-title">Precio por persona</h2>
        <p className="muted small">
          Dividí el costo total entre las personas y sumá un margen opcional. Se redondea hacia arriba a múltiplos de $ 100.
        </p>
      </div>

      <form noValidate aria-labelledby="calc-person-title" onSubmit={(event) => event.preventDefault()}>
        <Field
          label="Costo total ($)"
          name="personTotalCost"
          type="text"
          inputMode="decimal"
          autoComplete="off"
          enterKeyHint="next"
          hint="Ej.: 180000 o 180.000"
          value={totalCost}
          error={errors.totalCost}
          onChange={(event) => {
            setInteracted(true);
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
          error={errors.peopleCount}
          onChange={(event) => {
            setInteracted(true);
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
          error={errors.marginPercent}
          onChange={(event) => {
            setInteracted(true);
            setMarginPercent(event.target.value);
          }}
        />
        <div className="calc-actions">
          <button type="button" className="button button--secondary button--small" onClick={reset}>
            Limpiar
          </button>
        </div>
      </form>

      <div className="calc-result" data-tone={outcome.ok ? "ok" : undefined} aria-live="polite" aria-atomic="true">
        {outcome.ok ? (
          <>
            <p className="calc-result__label">Precio por persona</p>
            <p className="calc-result__value">{outcome.result.formattedPricePerPerson}</p>
            <ul className="calc-result__details muted">
              <li>Sin redondeo: {outcome.result.formattedExactPerPerson}</li>
              <li>Total con margen: {outcome.result.formattedTotalWithMargin}</li>
            </ul>
          </>
        ) : (
          <p className="muted small">
            {interacted ? "Revisá los datos para ver el resultado." : "Completá los datos para calcular el precio por persona."}
          </p>
        )}
      </div>
    </section>
  );
}

function RaffleCard() {
  const [totalCost, setTotalCost] = useState("");
  const [unitPrice, setUnitPrice] = useState("");
  const [availableUnits, setAvailableUnits] = useState("");
  const [interacted, setInteracted] = useState(false);

  const outcome = calculateRaffle({ totalCost, unitPrice, availableUnits });
  const errors = interacted && !outcome.ok ? outcome.errors : {};

  function reset() {
    setTotalCost("");
    setUnitPrice("");
    setAvailableUnits("");
    setInteracted(false);
  }

  return (
    <section className="panel calc-panel" aria-labelledby="calc-raffle-title">
      <div className="calc-panel__header">
        <h2 id="calc-raffle-title">Rifa o venta</h2>
        <p className="muted small">
          Calculá cuántas unidades tenés que vender para no perder y cuánto ganás si vendés todo.
        </p>
      </div>

      <form noValidate aria-labelledby="calc-raffle-title" onSubmit={(event) => event.preventDefault()}>
        <Field
          label="Costo total de premios o mercadería ($)"
          name="raffleTotalCost"
          type="text"
          inputMode="decimal"
          autoComplete="off"
          enterKeyHint="next"
          hint="Ej.: 60000"
          value={totalCost}
          error={errors.totalCost}
          onChange={(event) => {
            setInteracted(true);
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
          error={errors.unitPrice}
          onChange={(event) => {
            setInteracted(true);
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
          error={errors.availableUnits}
          onChange={(event) => {
            setInteracted(true);
            setAvailableUnits(event.target.value);
          }}
        />
        <div className="calc-actions">
          <button type="button" className="button button--secondary button--small" onClick={reset}>
            Limpiar
          </button>
        </div>
      </form>

      <div
        className="calc-result"
        data-tone={outcome.ok && outcome.result.coversCost ? "ok" : undefined}
        aria-live="polite"
        aria-atomic="true"
      >
        {outcome.ok ? (
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
        ) : (
          <p className="muted small">
            {interacted ? "Revisá los datos para ver el resultado." : "Completá los datos para calcular la rifa o venta."}
          </p>
        )}
      </div>
    </section>
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
