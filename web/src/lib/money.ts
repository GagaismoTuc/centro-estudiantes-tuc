/** Formats integer cents as Argentine pesos (e.g. 2500000 -> "$25.000"). */
export function formatPesos(cents: number): string {
  const whole = Math.floor(cents / 100);
  const rem = cents % 100;
  const formattedWhole = whole.toLocaleString("es-AR");
  return rem === 0 ? `$${formattedWhole}` : `$${formattedWhole},${String(rem).padStart(2, "0")}`;
}
