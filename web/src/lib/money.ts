/** Formats integer cents as Argentine pesos (e.g. 2500000 -> "$25.000"). */
export function formatPesos(cents: number): string {
  const whole = Math.floor(cents / 100);
  const rem = cents % 100;
  const formattedWhole = new Intl.NumberFormat("es-AR").format(whole);
  return rem === 0 ? `$${formattedWhole}` : `$${formattedWhole},${String(rem).padStart(2, "0")}`;
}

/** Formats an integer centavos string as Argentine pesos, e.g. `$ 2.000,00`. */
export function pesos(value: string): string {
  const cents = BigInt(value);
  const negative = cents < 0n;
  const absolute = negative ? -cents : cents;
  return `$ ${negative ? "-" : ""}${new Intl.NumberFormat("es-AR").format(absolute / 100n)},${String(absolute % 100n).padStart(2, "0")}`;
}

/** Parses a positive peso decimal input (`2000` or `2000,50`) into a positive int64 centavos string. */
export function centsOf(value: string): string | null {
  const match = /^(\d+)(?:[,.](\d{1,2}))?$/.exec(value.trim());
  if (!match || match[1]!.length > 17) return null;
  const cents = BigInt(match[1]!) * 100n + BigInt((match[2] ?? "").padEnd(2, "0"));
  if (cents <= 0n || cents > 9223372036854775807n) return null;
  return String(cents);
}

/** Converts an integer centavos string into editable peso text (`2000` or `2000,50`). */
export function pesosInput(amountCents: string): string {
  const cents = BigInt(amountCents);
  const whole = cents / 100n;
  const frac = cents % 100n;
  return frac === 0n ? String(whole) : `${whole},${String(frac).padStart(2, "0")}`;
}
