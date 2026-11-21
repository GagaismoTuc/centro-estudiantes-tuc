import { describe, expect, test } from "vitest";
import {
  calculateDueAmount,
  calculatePricePerPerson,
  calculateRaffle,
  calculateSpendable,
  multiplyCents,
  parsePesosToCents,
  roundUpTo100Pesos,
} from "./calculator.ts";
import { formatPesos } from "./money.ts";

describe("R1 — Precio por persona", () => {
  test("computes base example, margin, ceiling to $100, and rejects empty or negative inputs", () => {
    const base = calculatePricePerPerson({ totalCost: "180000", peopleCount: "45" });
    expect(base).toEqual({
      ok: true,
      result: {
        pricePerPersonCents: 400_000,
        exactPerPersonCents: 400_000,
        totalWithMarginCents: 18_000_000,
        formattedPricePerPerson: "$ 4.000,00",
        formattedExactPerPerson: "$ 4.000,00",
        formattedTotalWithMargin: "$ 180.000,00",
      },
    });

    const withMargin = calculatePricePerPerson({ totalCost: "180.000", peopleCount: "45", marginPercent: "10" });
    expect(withMargin.ok).toBe(true);
    if (withMargin.ok) {
      expect(withMargin.result.formattedPricePerPerson).toBe("$ 4.400,00");
      expect(withMargin.result.formattedTotalWithMargin).toBe("$ 198.000,00");
    }

    const rounded = calculatePricePerPerson({ totalCost: "100000", peopleCount: "30" });
    expect(rounded.ok).toBe(true);
    if (rounded.ok) {
      expect(rounded.result.formattedExactPerPerson).toBe("$ 3.333,33");
      expect(rounded.result.formattedPricePerPerson).toBe("$ 3.400,00");
    }

    expect(calculatePricePerPerson({ totalCost: "180000", peopleCount: "0" })).toEqual({
      ok: false,
      errors: { peopleCount: "Ingresá cuántas personas van" },
    });
    expect(calculatePricePerPerson({ totalCost: "180000", peopleCount: "" })).toEqual({
      ok: false,
      errors: { peopleCount: "Ingresá cuántas personas van" },
    });
    expect(calculatePricePerPerson({ totalCost: "180000", peopleCount: "-5" })).toEqual({
      ok: false,
      errors: { peopleCount: "Ingresá cuántas personas van" },
    });
    expect(calculatePricePerPerson({ totalCost: "", peopleCount: "45" })).toEqual({
      ok: false,
      errors: { totalCost: "Ingresá el costo total" },
    });
    expect(calculatePricePerPerson({ totalCost: "-100", peopleCount: "45" })).toEqual({
      ok: false,
      errors: { totalCost: "Ingresá el costo total" },
    });
    expect(calculatePricePerPerson({ totalCost: "180000", peopleCount: "45", marginPercent: "-5" })).toEqual({
      ok: false,
      errors: { marginPercent: "El margen no puede ser negativo" },
    });
  });
});

describe("R2 — Rifa o venta", () => {
  test("computes break-even units, profit, loss warning, ceiling, and rejects invalid unit price", () => {
    const profitable = calculateRaffle({ totalCost: "60000", unitPrice: "1500", availableUnits: "200" });
    expect(profitable).toEqual({
      ok: true,
      result: {
        breakEvenUnits: 40,
        maxRevenueCents: 30_000_000,
        profitIfAllSoldCents: 24_000_000,
        coversCost: true,
        formattedMaxRevenue: "$ 300.000,00",
        formattedProfitOrLoss: "$ 240.000,00",
        lossMessage: null,
      },
    });

    const loss = calculateRaffle({ totalCost: "400.000", unitPrice: "1.500", availableUnits: "200" });
    expect(loss.ok).toBe(true);
    if (loss.ok) {
      expect(loss.result.coversCost).toBe(false);
      expect(loss.result.breakEvenUnits).toBe(267);
      expect(loss.result.profitIfAllSoldCents).toBe(-10_000_000);
      expect(loss.result.lossMessage).toBe("Ni vendiendo todo cubrís el costo: perdés $ 100.000,00");
    }

    const ceiled = calculateRaffle({ totalCost: "50000", unitPrice: "1500", availableUnits: "100" });
    expect(ceiled.ok).toBe(true);
    if (ceiled.ok) {
      expect(ceiled.result.breakEvenUnits).toBe(34);
    }

    expect(calculateRaffle({ totalCost: "60000", unitPrice: "0", availableUnits: "200" })).toEqual({
      ok: false,
      errors: { unitPrice: "Ingresá el precio por unidad" },
    });
    expect(calculateRaffle({ totalCost: "60000", unitPrice: "", availableUnits: "200" })).toEqual({
      ok: false,
      errors: { unitPrice: "Ingresá el precio por unidad" },
    });
    expect(calculateRaffle({ totalCost: "", unitPrice: "1500", availableUnits: "200" })).toEqual({
      ok: false,
      errors: { totalCost: "Ingresá el costo total" },
    });
    expect(calculateRaffle({ totalCost: "60000", unitPrice: "1500", availableUnits: "" })).toEqual({
      ok: false,
      errors: { availableUnits: "Ingresá cuántas unidades hay disponibles" },
    });
  });
});

describe("R3 — Redondeo y centavos enteros", () => {
  test("rounds up to $100 leaving exact multiples unchanged and avoids floating-point error on 0,10 x 3", () => {
    expect(roundUpTo100Pesos(400_000)).toBe(400_000);
    expect(formatPesos(roundUpTo100Pesos(400_000))).toBe("$ 4.000,00");
    expect(roundUpTo100Pesos(400_001)).toBe(410_000);
    expect(roundUpTo100Pesos(0)).toBe(0);

    // 3000 * 1.10 in IEEE-754 float is 3300.0000000000005; exact cents stay $ 3.300,00.
    const exactMargin = calculatePricePerPerson({ totalCost: "3000", peopleCount: "1", marginPercent: "10" });
    expect(exactMargin.ok).toBe(true);
    if (exactMargin.ok) {
      expect(exactMargin.result.formattedPricePerPerson).toBe("$ 3.300,00");
    }

    const parsed = parsePesosToCents("0,10");
    expect(parsed).toEqual({ ok: true, cents: 10 });
    if (parsed.ok) {
      expect(multiplyCents(parsed.cents, 3)).toBe(30);
      expect(formatPesos(multiplyCents(parsed.cents, 3))).toBe("$ 0,30");
    }

    const raffleCents = calculateRaffle({ totalCost: "0", unitPrice: "0,10", availableUnits: "3" });
    expect(raffleCents.ok).toBe(true);
    if (raffleCents.ok) {
      expect(raffleCents.result.maxRevenueCents).toBe(30);
      expect(raffleCents.result.formattedProfitOrLoss).toBe("$ 0,30");
    }
  });
});

describe("R2 (#84) — Monto de Cuota", () => {
  test("computes due amount from goal and roster with optional expected exemptions and rejects invalid inputs", () => {
    // Goal $600.000, roster 480, default 0 exempt -> exact $1.250, rounded up to $100 -> $1.300.
    const base = calculateDueAmount({ goal: "600000", rosterCount: 480 });
    expect(base).toEqual({
      ok: true,
      result: {
        payingCount: 480,
        exemptCount: 0,
        amountPerStudentCents: 130_000,
        exactPerStudentCents: 125_000,
        formattedAmountPerStudent: "$ 1.300,00",
        formattedExactPerStudent: "$ 1.250,00",
      },
    });

    // Goal $600.000, roster 480, 30 expected exempt (450 paying) -> $1.333,33 -> $1.400.
    const withExempt = calculateDueAmount({ goal: "600.000", rosterCount: 480, expectedExempt: "30" });
    expect(withExempt.ok).toBe(true);
    if (withExempt.ok) {
      expect(withExempt.result.payingCount).toBe(450);
      expect(withExempt.result.exemptCount).toBe(30);
      expect(withExempt.result.formattedExactPerStudent).toBe("$ 1.333,33");
      expect(withExempt.result.formattedAmountPerStudent).toBe("$ 1.400,00");
    }

    // Exact multiple of $100 stays unchanged ($480.000 / 480 = $1.000).
    const exactMultiple = calculateDueAmount({ goal: "480000", rosterCount: 480, expectedExempt: "0" });
    expect(exactMultiple.ok).toBe(true);
    if (exactMultiple.ok) {
      expect(exactMultiple.result.formattedAmountPerStudent).toBe("$ 1.000,00");
    }

    // Negative cases: empty/0 goal, negative exempt, empty roster.
    for (const invalid of [
      { input: { goal: "", rosterCount: 480, expectedExempt: "0" }, field: "goal" as const },
      { input: { goal: "0", rosterCount: 480, expectedExempt: "0" }, field: "goal" as const },
      { input: { goal: "-1000", rosterCount: 480, expectedExempt: "0" }, field: "goal" as const },
      { input: { goal: "600000", rosterCount: 0, expectedExempt: "0" }, field: "goal" as const },
      { input: { goal: "600000", rosterCount: 480, expectedExempt: "-1" }, field: "expectedExempt" as const },
    ]) {
      const result = calculateDueAmount(invalid.input);
      expect(result.ok).toBe(false);
      if (!result.ok) {
        expect(result.error).toBe("Revisá la meta y la cantidad de alumnos");
        expect(result.errors[invalid.field]).toBe("Revisá la meta y la cantidad de alumnos");
      }
    }
  });
});

describe("R4 (#84) — Cuánto podemos gastar", () => {
  test("computes spendable totals, planned cost shortfall/surplus, and negative balance", () => {
    // Balance $150.000 + owed $316.000 (158 * $2.000) = $466.000.
    const base = calculateSpendable({ balanceCents: "15000000", owedOnDuesCents: "31600000" });
    expect(base.formattedBalance).toBe("$ 150.000,00");
    expect(base.formattedOwedOnDues).toBe("$ 316.000,00");
    expect(base.formattedTotalWithOwed).toBe("$ 466.000,00");
    expect(base.summaryMessage).toBe("Hoy tenés $ 150.000,00; si cobrás lo que falta, $ 466.000,00");
    expect(base.comparison).toBeNull();
    expect(base.plannedCostError).toBeUndefined();

    // Planned cost $500.000 -> shortfall $34.000.
    const short = calculateSpendable({
      balanceCents: "15000000",
      owedOnDuesCents: "31600000",
      plannedCost: "500.000",
    });
    expect(short.comparison).toEqual({
      status: "short",
      differenceCents: "-3400000",
      message: "Aunque cobres todo, te faltan $ 34.000,00",
    });

    // Planned cost covered today ($100.000 -> surplus today $50.000).
    const coveredToday = calculateSpendable({
      balanceCents: "15000000",
      owedOnDuesCents: "31600000",
      plannedCost: "100000",
    });
    expect(coveredToday.comparison?.status).toBe("covered_today");
    expect(coveredToday.comparison?.message).toBe("Te alcanza hoy y te sobran $ 50.000,00");

    // Planned cost covered only if dues are collected ($300.000 -> short today $150.000, surplus with dues $166.000).
    const coveredIfCollected = calculateSpendable({
      balanceCents: "15000000",
      owedOnDuesCents: "31600000",
      plannedCost: "300000",
    });
    expect(coveredIfCollected.comparison?.status).toBe("covered_if_collected");
    expect(coveredIfCollected.comparison?.message).toBe(
      "Hoy te faltan $ 150.000,00, pero si cobrás lo que falta te sobran $ 166.000,00",
    );

    // Negative balance is displayed in negative.
    const negative = calculateSpendable({ balanceCents: "-2000000", owedOnDuesCents: "1000000" });
    expect(negative.formattedBalance).toBe("-$ 20.000,00");
    expect(negative.formattedTotalWithOwed).toBe("-$ 10.000,00");
    expect(negative.summaryMessage).toBe("Hoy tenés -$ 20.000,00; si cobrás lo que falta, -$ 10.000,00");

    // Values above Number.MAX_SAFE_INTEGER keep exact BigInt precision.
    const huge = calculateSpendable({
      balanceCents: "9007199254740993",
      owedOnDuesCents: "200",
      plannedCost: "10",
    });
    expect(huge.totalWithOwedCents).toBe("9007199254741193");
    expect(huge.summaryMessage).toBe(
      "Hoy tenés $ 90.071.992.547.409,93; si cobrás lo que falta, $ 90.071.992.547.411,93",
    );
    expect(huge.comparison?.message).toBe("Te alcanza hoy y te sobran $ 90.071.992.547.399,93");

    // Invalid planned cost surfaces an error without breaking the summary.
    const badCost = calculateSpendable({
      balanceCents: "15000000",
      owedOnDuesCents: "31600000",
      plannedCost: "-100",
    });
    expect(badCost.comparison).toBeNull();
    expect(badCost.plannedCostError).toBe("Ingresá un costo planeado válido");
  });
});

describe("R9 (#134) — exentos y padrón", () => {
  test("says the exempt count cannot exceed the roster when it is equal or larger", () => {
    for (const expectedExempt of ["480", "500"]) {
      const result = calculateDueAmount({ goal: "600000", rosterCount: 480, expectedExempt });
      expect(result.ok).toBe(false);
      if (!result.ok) {
        expect(result.error).toBe("Los exentos no pueden ser más que los alumnos del padrón.");
        expect(result.errors).toEqual({ expectedExempt: "Los exentos no pueden ser más que los alumnos del padrón." });
      }
    }
    const below = calculateDueAmount({ goal: "600000", rosterCount: 480, expectedExempt: "479" });
    expect(below.ok).toBe(true);
  });
});
