const PESO_CENTS = 100n;
const ROUND_STEP_CENTS = 10_000n; // $100 in integer cents
const BPS_SCALE = 10_000n; // 100.00% = 10_000 basis points

export type ParseCentsOutcome =
  | { ok: true; cents: number }
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

/**
 * Parses a decimal string (supporting Argentine `,` decimals and `.` thousand separators)
 * into a scaled integer with `decimals` fractional digits without floating-point arithmetic.
 */
function parseDecimalScaled(raw: string, prefixPattern: RegExp, suffixPattern: RegExp): ParseCentsOutcome {
  const trimmed = raw.trim().replace(prefixPattern, "").replace(suffixPattern, "").trim();
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
  const scaled = whole * PESO_CENTS + fracTwo + roundUp;
  if (scaled > BigInt(Number.MAX_SAFE_INTEGER)) return { ok: false, reason: "invalid" };

  return { ok: true, cents: Number(scaled) };
}

/** Parses a monetary peso string into integer centavos. */
export function parsePesosToCents(raw: string): ParseCentsOutcome {
  return parseDecimalScaled(raw, /^\$\s*/, /^$/);
}

function parsePositiveInteger(raw: string): number | null {
  const trimmed = raw.trim();
  if (!/^\d+$/.test(trimmed)) return null;
  const value = Number(trimmed);
  if (!Number.isSafeInteger(value) || value <= 0) return null;
  return value;
}

/** Multiplies integer centavos by an integer quantity without floating-point error. */
export function multiplyCents(cents: number, quantity: number): number {
  return Number(BigInt(Math.trunc(cents)) * BigInt(Math.trunc(quantity)));
}

/** Rounds non-negative integer centavos up to the nearest multiple of $100 (10,000 cents). */
export function roundUpTo100Pesos(cents: number): number {
  if (cents <= 0) return 0;
  const c = BigInt(Math.trunc(cents));
  return Number(((c + ROUND_STEP_CENTS - 1n) / ROUND_STEP_CENTS) * ROUND_STEP_CENTS);
}

/** Formats integer centavos as `$ 4.400` (omitting `,00` for whole pesos, keeping `,cc` otherwise). */
export function formatPesos(cents: number): string {
  const rounded = Math.trunc(cents);
  const sign = rounded < 0 ? "-" : "";
  const abs = Math.abs(rounded);
  const whole = Math.floor(abs / 100);
  const frac = abs % 100;
  const wholeFormatted = String(whole).replace(/\B(?=(\d{3})+(?!\d))/g, ".");
  const fracFormatted = frac === 0 ? "" : `,${String(frac).padStart(2, "0")}`;
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
  const rawMargin = (input.marginPercent ?? "").trim();
  if (rawMargin !== "") {
    const parsedMargin = parseDecimalScaled(rawMargin, /^$/, /\s*%$/);
    if (!parsedMargin.ok) {
      errors.marginPercent =
        parsedMargin.reason === "negative" ? "El margen no puede ser negativo" : "Ingresá un margen válido";
    } else {
      marginBps = BigInt(parsedMargin.cents);
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
