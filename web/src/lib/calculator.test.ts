import { describe, expect, test } from "vitest";
import {
  calculatePricePerPerson,
  calculateRaffle,
  formatPesos,
  multiplyCents,
  parsePesosToCents,
  roundUpTo100Pesos,
} from "./calculator.ts";

describe("R1 — Precio por persona", () => {
  test("computes base example, margin, ceiling to $100, and rejects empty or negative inputs", () => {
    const base = calculatePricePerPerson({ totalCost: "180000", peopleCount: "45" });
    expect(base).toEqual({
      ok: true,
      result: {
        pricePerPersonCents: 400_000,
        exactPerPersonCents: 400_000,
        totalWithMarginCents: 18_000_000,
        formattedPricePerPerson: "$ 4.000",
        formattedExactPerPerson: "$ 4.000",
        formattedTotalWithMargin: "$ 180.000",
      },
    });

    const withMargin = calculatePricePerPerson({ totalCost: "180.000", peopleCount: "45", marginPercent: "10" });
    expect(withMargin.ok).toBe(true);
    if (withMargin.ok) {
      expect(withMargin.result.formattedPricePerPerson).toBe("$ 4.400");
      expect(withMargin.result.formattedTotalWithMargin).toBe("$ 198.000");
    }

    const rounded = calculatePricePerPerson({ totalCost: "100000", peopleCount: "30" });
    expect(rounded.ok).toBe(true);
    if (rounded.ok) {
      expect(rounded.result.formattedExactPerPerson).toBe("$ 3.333,33");
      expect(rounded.result.formattedPricePerPerson).toBe("$ 3.400");
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
        formattedMaxRevenue: "$ 300.000",
        formattedProfitOrLoss: "$ 240.000",
        lossMessage: null,
      },
    });

    const loss = calculateRaffle({ totalCost: "400.000", unitPrice: "1.500", availableUnits: "200" });
    expect(loss.ok).toBe(true);
    if (loss.ok) {
      expect(loss.result.coversCost).toBe(false);
      expect(loss.result.breakEvenUnits).toBe(267);
      expect(loss.result.profitIfAllSoldCents).toBe(-10_000_000);
      expect(loss.result.lossMessage).toBe("Ni vendiendo todo cubrís el costo: perdés $ 100.000");
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
    expect(formatPesos(roundUpTo100Pesos(400_000))).toBe("$ 4.000");
    expect(roundUpTo100Pesos(400_001)).toBe(410_000);
    expect(roundUpTo100Pesos(0)).toBe(0);

    // 3000 * 1.10 in IEEE-754 float is 3300.0000000000005; exact cents stay $ 3.300.
    const exactMargin = calculatePricePerPerson({ totalCost: "3000", peopleCount: "1", marginPercent: "10" });
    expect(exactMargin.ok).toBe(true);
    if (exactMargin.ok) {
      expect(exactMargin.result.formattedPricePerPerson).toBe("$ 3.300");
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
