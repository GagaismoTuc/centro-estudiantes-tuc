export type MoneyResult<T> = { ok: true; data: T } | { ok: false; code: string };

// Preserve the decimal tokens before JSON.parse rounds integers beyond 2^53.
export function exactMoneyJSON(text: string): unknown {
  if (!text) return undefined;
  let out = "";
  for (let i = 0; i < text.length;) {
    if (text[i] !== '"') { out += text[i++]; continue; }
    const start = i++;
    while (i < text.length) {
      if (text[i] === "\\") { i += 2; continue; }
      if (text[i++] === '"') break;
    }
    const token = text.slice(start, i);
    out += token;
    let j = i;
    while (/\s/.test(text[j] ?? "")) j++;
    if (text[j] !== ":" || (token !== '"amountCents"' && token !== '"balanceCents"')) continue;
    j++;
    while (/\s/.test(text[j] ?? "")) j++;
    const number = /^-?\d+/.exec(text.slice(j));
    if (!number) continue;
    out += text.slice(i, j) + JSON.stringify(number[0]);
    i = j + number[0].length;
  }
  return JSON.parse(out);
}

export async function moneyRequest<T>(path: string, init?: RequestInit): Promise<MoneyResult<T>> {
  try {
    const response = await fetch(new Request(new URL(`/api${path}`, window.location.origin), init));
    const body = exactMoneyJSON(await response.text()) as (T & { error?: string }) | undefined;
    return response.ok ? { ok: true, data: body as T } : { ok: false, code: body?.error ?? "server_error" };
  } catch {
    return { ok: false, code: "network" };
  }
}

export function isValidInt64Cents(amountCents: string): boolean {
  return /^[1-9]\d{0,18}$/.test(amountCents) && BigInt(amountCents) <= 9223372036854775807n;
}
