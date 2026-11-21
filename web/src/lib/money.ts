/** Formats integer centavos as Argentine pesos: always two decimals, e.g. `$ 300.001,50` and `-$ 195.269,88`. */
export function formatPesos(cents: bigint | number | string): string {
  const value = BigInt(cents);
  const absolute = value < 0n ? -value : value;
  const whole = (absolute / 100n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ".");
  return `${value < 0n ? "-" : ""}$ ${whole},${String(absolute % 100n).padStart(2, "0")}`;
}

/** Formats an ISO `YYYY-MM-DD` date as `DD/MM/YYYY`. */
export function dayMonthYear(isoDate: string): string {
  return isoDate.split("-").reverse().join("/");
}

/** Returns today's `YYYY-MM-DD` date in Argentina time. */
export function todayArgentina(): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "America/Argentina/Buenos_Aires",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date());
}

const MAX_CENTS = 9223372036854775807n; // int64, the API's centavos range

// A dot followed by exactly three digits groups thousands; the decimals are a comma, or a final dot, and one or two digits.
const AMOUNT = /^\$?\s*(?:(\d{1,3}(?:\.\d{3})+)(?:,(\d{1,2}))?|(\d+)(?:[.,](\d{1,2}))?)$/;

/** Parses typed peso text (`1.234,56`, `1234.56`, `600.000`) into non-negative centavos, or null when it is not an amount. */
export function parseHundredths(raw: string): bigint | null {
  const match = AMOUNT.exec(raw.trim());
  if (!match) return null;
  const whole = (match[1] ?? match[3]!).replaceAll(".", "");
  const cents = BigInt(whole) * 100n + BigInt((match[2] ?? match[4] ?? "").padEnd(2, "0"));
  return cents <= MAX_CENTS ? cents : null;
}

/** Parses a peso amount typed in a form into positive centavos, or null when it must be rejected. */
export function parsePesos(raw: string): bigint | null {
  const cents = parseHundredths(raw);
  return cents !== null && cents > 0n ? cents : null;
}

/** Like `parsePesos`, for contracts that carry centavos as a JSON number: rejects what a number cannot hold exactly. */
export function parsePesosNumber(raw: string): number | null {
  const cents = parsePesos(raw);
  return cents !== null && cents <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(cents) : null;
}

/** Converts integer centavos into editable peso text (`2000` or `2000,50`). */
export function pesosInput(amountCents: bigint | number | string): string {
  const cents = BigInt(amountCents);
  const whole = cents / 100n;
  const frac = cents % 100n;
  return frac === 0n ? String(whole) : `${whole},${String(frac).padStart(2, "0")}`;
}
