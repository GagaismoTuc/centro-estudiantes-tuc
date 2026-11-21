const PESO_CENTS = 100n;
const ROUND_STEP_CENTS = 10_000n; // $100 in integer cents
const BPS_SCALE = 10_000n; // 100.00% = 10_000 basis points

export type ParseCentsOutcome =
  | { ok: true; cents: number }
  | { ok: false; reason: "empty" | "negative" | "invalid" };

type ParseHundredthsOutcome =
  | { ok: true; hundredths: number }
  | { ok: false; reason: "empty" | "negative" | "invalid" };

export type PricePerPersonErrors = {
  totalCost?: string;
  peopleCount?: string;
  marginPercent?: string;
};

export type PricePerPersonResult = {
  pricePerPersonCents: number;
  exactPerPersonCents: number;
  totalWithMarginCents: number;
  formattedPricePerPerson: string;
  formattedExactPerPerson: string;
  formattedTotalWithMargin: string;
};

export type PricePerPersonOutcome =
  | { ok: true; result: PricePerPersonResult }
  | { ok: false; errors: PricePerPersonErrors };

export type RaffleErrors = {
  totalCost?: string;
  unitPrice?: string;
  availableUnits?: string;
};

export type RaffleResult = {
  breakEvenUnits: number;
  maxRevenueCents: number;
  profitIfAllSoldCents: number;
  coversCost: boolean;
  formattedMaxRevenue: string;
  formattedProfitOrLoss: string;
  lossMessage: string | null;
};

export type RaffleOutcome =
  | { ok: true; result: RaffleResult }
  | { ok: false; errors: RaffleErrors };

export type DueAmountErrors = {
  goal?: string;
  expectedExempt?: string;
};

export type DueAmountResult = {
  payingCount: number;
  exemptCount: number;
  amountPerStudentCents: number;
  exactPerStudentCents: number;
  formattedAmountPerStudent: string;
  formattedExactPerStudent: string;
};

export type DueAmountOutcome =
  | { ok: true; result: DueAmountResult }
  | { ok: false; error: string; errors: DueAmountErrors };

export type SpendableComparison = {
  status: "short" | "covered_today" | "covered_if_collected";
  differenceCents: string;
  message: string;
};

export type SpendableResult = {
  balanceCents: string;
  owedOnDuesCents: string;
  totalWithOwedCents: string;
  formattedBalance: string;
  formattedOwedOnDues: string;
  formattedTotalWithOwed: string;
  summaryMessage: string;
  comparison: SpendableComparison | null;
  plannedCostError?: string | undefined;
};

/**
 * Parses a decimal string (supporting Argentine `,` decimals and `.` thousand separators)
 * into integer hundredths without floating-point arithmetic.
 */
function parseDecimalHundredths(raw: string): ParseHundredthsOutcome {
  const trimmed = raw.trim();
  if (!trimmed) return { ok: false, reason: "empty" };

  const isNegative = trimmed.startsWith("-");
  const unsigned = isNegative ? trimmed.slice(1).trim() : trimmed;
  if (!unsigned || /[^0-9.,]/.test(unsigned)) return { ok: false, reason: "invalid" };

  const hasDot = unsigned.includes(".");
  const hasComma = unsigned.includes(",");
  let normalized: string;

  if (hasDot && hasComma) {
    if (unsigned.lastIndexOf(",") > unsigned.lastIndexOf(".")) {
      if ((unsigned.match(/,/g) ?? []).length > 1) return { ok: false, reason: "invalid" };
      normalized = unsigned.replaceAll(".", "").replace(",", ".");
    } else {
      if ((unsigned.match(/\./g) ?? []).length > 1) return { ok: false, reason: "invalid" };
      normalized = unsigned.replaceAll(",", "");
    }
  } else if (hasComma) {
    const commaCount = (unsigned.match(/,/g) ?? []).length;
    if (commaCount > 1) {
      if (!/^\d{1,3}(,\d{3})+$/.test(unsigned)) return { ok: false, reason: "invalid" };
      normalized = unsigned.replaceAll(",", "");
    } else {
      normalized = unsigned.replace(",", ".");
    }
  } else if (hasDot) {
    const dotCount = (unsigned.match(/\./g) ?? []).length;
    if (dotCount > 1) {
      if (!/^\d{1,3}(\.\d{3})+$/.test(unsigned)) return { ok: false, reason: "invalid" };
      normalized = unsigned.replaceAll(".", "");
    } else if (/^[1-9]\d{0,2}\.\d{3}$/.test(unsigned)) {
      normalized = unsigned.replace(".", "");
    } else {
      normalized = unsigned;
    }
  } else {
    normalized = unsigned;
  }

  if (!/^\d+(\.\d+)?$/.test(normalized)) return { ok: false, reason: "invalid" };
  if (isNegative) return { ok: false, reason: "negative" };

  const [wholeStr, fracStr = ""] = normalized.split(".");
  const whole = BigInt(wholeStr ?? "0");
  const fracTwo = BigInt(fracStr.slice(0, 2).padEnd(2, "0"));
  const roundUp = Number(fracStr[2] ?? "0") >= 5 ? 1n : 0n;
  const hundredths = whole * PESO_CENTS + fracTwo + roundUp;
  if (hundredths > BigInt(Number.MAX_SAFE_INTEGER)) return { ok: false, reason: "invalid" };

  return { ok: true, hundredths: Number(hundredths) };
}

/** Parses a monetary peso string into integer centavos. */
export function parsePesosToCents(raw: string): ParseCentsOutcome {
  const withoutCurrency = raw.trim().replace(/^\$\s*/, "");
  const parsed = parseDecimalHundredths(withoutCurrency);
  return parsed.ok ? { ok: true, cents: parsed.hundredths } : parsed;
}

function parseInteger(raw: string, min: number): number | null {
  const trimmed = raw.trim();
  if (!/^\d+$/.test(trimmed)) return null;
  const value = Number(trimmed);
  if (!Number.isSafeInteger(value) || value < min) return null;
  return value;
}

function parsePositiveInteger(raw: string): number | null {
  return parseInteger(raw, 1);
}

function parseNonNegativeInteger(raw: string): number | null {
  return parseInteger(raw, 0);
}

// shortcut: JS safe integer centavos (up to ~$90 trillion ARS); return BigInt if multi-trillion amounts are ever needed.
/** Multiplies integer centavos by an integer quantity without floating-point error. */
export function multiplyCents(cents: number, quantity: number): number {
  const product = BigInt(Math.trunc(cents)) * BigInt(Math.trunc(quantity));
  return Number(product);
}

/** Rounds non-negative integer centavos up to the nearest multiple of $100 (10,000 cents). */
export function roundUpTo100Pesos(cents: number): number {
  if (cents <= 0) return 0;
  const c = BigInt(Math.trunc(cents));
  return Number(((c + ROUND_STEP_CENTS - 1n) / ROUND_STEP_CENTS) * ROUND_STEP_CENTS);
}

/** Formats integer centavos as `$ 4.400` (omitting `,00` for whole pesos, keeping `,cc` otherwise). */
export function formatPesos(cents: number | bigint): string {
  const value = typeof cents === "bigint" ? cents : BigInt(Math.trunc(cents));
  const sign = value < 0n ? "-" : "";
  const abs = value < 0n ? -value : value;
  const whole = abs / PESO_CENTS;
  const frac = abs % PESO_CENTS;
  const wholeFormatted = whole.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ".");
  const fracFormatted = frac === 0n ? "" : `,${frac.toString().padStart(2, "0")}`;
  return `${sign}$ ${wholeFormatted}${fracFormatted}`;
}

/** Calculates price per person rounded up to $100, with optional percentage margin. */
export function calculatePricePerPerson(input: {
  totalCost: string;
  peopleCount: string;
  marginPercent?: string;
}): PricePerPersonOutcome {
  const errors: PricePerPersonErrors = {};

  const cost = parsePesosToCents(input.totalCost);
  if (!cost.ok) errors.totalCost = "Ingresá el costo total";

  const people = parsePositiveInteger(input.peopleCount);
  if (people === null) errors.peopleCount = "Ingresá cuántas personas van";

  let marginBps = 0n;
  const rawMargin = (input.marginPercent ?? "").trim().replace(/\s*%$/, "");
  if (rawMargin !== "") {
    const parsedMargin = parseDecimalHundredths(rawMargin);
    if (!parsedMargin.ok) {
      errors.marginPercent =
        parsedMargin.reason === "negative" ? "El margen no puede ser negativo" : "Ingresá un margen válido";
    } else {
      marginBps = BigInt(parsedMargin.hundredths);
    }
  }

  if (Object.keys(errors).length > 0 || !cost.ok || people === null) {
    return { ok: false, errors };
  }

  const totalNum = BigInt(cost.cents) * (BPS_SCALE + marginBps);
  const totalWithMarginCents = Number((totalNum + BPS_SCALE / 2n) / BPS_SCALE);

  const perPersonDen = BPS_SCALE * BigInt(people);
  const exactPerPersonCents = Number((totalNum + perPersonDen / 2n) / perPersonDen);

  const step = ROUND_STEP_CENTS * perPersonDen;
  const pricePerPersonCents = Number(((totalNum + step - 1n) / step) * ROUND_STEP_CENTS);

  return {
    ok: true,
    result: {
      pricePerPersonCents,
      exactPerPersonCents,
      totalWithMarginCents,
      formattedPricePerPerson: formatPesos(pricePerPersonCents),
      formattedExactPerPerson: formatPesos(exactPerPersonCents),
      formattedTotalWithMargin: formatPesos(totalWithMarginCents),
    },
  };
}

/** Calculates break-even units (ceiling) and profit/loss if all units are sold. */
export function calculateRaffle(input: {
  totalCost: string;
  unitPrice: string;
  availableUnits: string;
}): RaffleOutcome {
  const errors: RaffleErrors = {};

  const cost = parsePesosToCents(input.totalCost);
  if (!cost.ok) errors.totalCost = "Ingresá el costo total";

  const price = parsePesosToCents(input.unitPrice);
  if (!price.ok || price.cents <= 0) errors.unitPrice = "Ingresá el precio por unidad";

  const units = parsePositiveInteger(input.availableUnits);
  if (units === null) errors.availableUnits = "Ingresá cuántas unidades hay disponibles";

  if (Object.keys(errors).length > 0 || !cost.ok || !price.ok || price.cents <= 0 || units === null) {
    return { ok: false, errors };
  }

  const costBig = BigInt(cost.cents);
  const priceBig = BigInt(price.cents);
  const breakEvenUnits = costBig === 0n ? 0 : Number((costBig + priceBig - 1n) / priceBig);

  const maxRevenueCents = multiplyCents(price.cents, units);
  const profitIfAllSoldCents = maxRevenueCents - cost.cents;
  const coversCost = profitIfAllSoldCents >= 0;
  const formattedProfitOrLoss = formatPesos(Math.abs(profitIfAllSoldCents));
  const lossMessage = coversCost
    ? null
    : `Ni vendiendo todo cubrís el costo: perdés ${formattedProfitOrLoss}`;

  return {
    ok: true,
    result: {
      breakEvenUnits,
      maxRevenueCents,
      profitIfAllSoldCents,
      coversCost,
      formattedMaxRevenue: formatPesos(maxRevenueCents),
      formattedProfitOrLoss,
      lossMessage,
    },
  };
}

const DUE_AMOUNT_ERROR = "Revisá la meta y la cantidad de alumnos";
const TOO_MANY_EXEMPT_ERROR = "Los exentos no pueden ser más que los alumnos del padrón.";

/**
 * Calculates the per-student Due amount from a target goal, the current roster count and an
 * optional count of expected exemptions (default 0), rounding up to multiples of $100.
 */
export function calculateDueAmount(input: {
  goal: string;
  rosterCount: number;
  expectedExempt?: string;
}): DueAmountOutcome {
  const errors: DueAmountErrors = {};
  const parsedGoal = parsePesosToCents(input.goal);
  const rosterValid = Number.isSafeInteger(input.rosterCount) && input.rosterCount > 0;
  if (!parsedGoal.ok || parsedGoal.cents <= 0 || !rosterValid) {
    errors.goal = DUE_AMOUNT_ERROR;
  }

  const rawExempt = (input.expectedExempt ?? "").trim();
  const exemptCount = rawExempt === "" ? 0 : parseNonNegativeInteger(rawExempt);
  if (exemptCount === null || !rosterValid) {
    errors.expectedExempt = DUE_AMOUNT_ERROR;
  } else if (exemptCount >= input.rosterCount) {
    errors.expectedExempt = TOO_MANY_EXEMPT_ERROR;
  }

  if (Object.keys(errors).length > 0 || !parsedGoal.ok || exemptCount === null) {
    return { ok: false, error: errors.goal ?? errors.expectedExempt ?? DUE_AMOUNT_ERROR, errors };
  }

  const payingCount = input.rosterCount - exemptCount;
  const goalBig = BigInt(parsedGoal.cents);
  const payingBig = BigInt(payingCount);

  const exactPerStudentCents = Number((goalBig + payingBig / 2n) / payingBig);
  const step = ROUND_STEP_CENTS * payingBig;
  const amountPerStudentCents = Number(((goalBig + step - 1n) / step) * ROUND_STEP_CENTS);

  return {
    ok: true,
    result: {
      payingCount,
      exemptCount,
      amountPerStudentCents,
      exactPerStudentCents,
      formattedAmountPerStudent: formatPesos(amountPerStudentCents),
      formattedExactPerStudent: formatPesos(exactPerStudentCents),
    },
  };
}

/**
 * Calculates how much the Student center can spend today (current Fund balance) and if all pending
 * Dues are collected, optionally comparing against a planned cost.
 */
export function calculateSpendable(input: {
  balanceCents: string;
  owedOnDuesCents: string;
  plannedCost?: string;
}): SpendableResult {
  const balance = BigInt(input.balanceCents);
  const owed = BigInt(input.owedOnDuesCents);
  const totalWithOwed = balance + owed;

  const formattedBalance = formatPesos(balance);
  const formattedOwedOnDues = formatPesos(owed);
  const formattedTotalWithOwed = formatPesos(totalWithOwed);
  const base = {
    balanceCents: balance.toString(),
    owedOnDuesCents: owed.toString(),
    totalWithOwedCents: totalWithOwed.toString(),
    formattedBalance,
    formattedOwedOnDues,
    formattedTotalWithOwed,
    summaryMessage: `Hoy tenés ${formattedBalance}; si cobrás lo que falta, ${formattedTotalWithOwed}`,
  };

  const rawPlanned = (input.plannedCost ?? "").trim();
  if (rawPlanned === "") {
    return { ...base, comparison: null };
  }

  const parsedPlanned = parsePesosToCents(rawPlanned);
  if (!parsedPlanned.ok) {
    return { ...base, comparison: null, plannedCostError: "Ingresá un costo planeado válido" };
  }

  const planned = BigInt(parsedPlanned.cents);
  const diffToday = balance - planned;
  const diffWithOwed = totalWithOwed - planned;

  let comparison: SpendableComparison;
  if (diffWithOwed < 0n) {
    comparison = {
      status: "short",
      differenceCents: diffWithOwed.toString(),
      message: `Aunque cobres todo, te faltan ${formatPesos(-diffWithOwed)}`,
    };
  } else if (diffToday >= 0n) {
    comparison = {
      status: "covered_today",
      differenceCents: diffToday.toString(),
      message:
        diffToday === 0n
          ? "Te alcanza justo con lo que tenés hoy"
          : `Te alcanza hoy y te sobran ${formatPesos(diffToday)}`,
    };
  } else {
    comparison = {
      status: "covered_if_collected",
      differenceCents: diffWithOwed.toString(),
      message:
        diffWithOwed === 0n
          ? `Hoy te faltan ${formatPesos(-diffToday)}, pero si cobrás lo que falta te alcanza justo`
          : `Hoy te faltan ${formatPesos(-diffToday)}, pero si cobrás lo que falta te sobran ${formatPesos(diffWithOwed)}`,
    };
  }

  return { ...base, comparison };
}
