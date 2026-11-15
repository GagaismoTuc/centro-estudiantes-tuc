import type { components } from "./schema.gen.ts";

type Entry = Omit<components["schemas"]["FinanceEntry"], "amountCents"> & { amountCents: string };
type Result<T> = { ok: true; data: T } | { ok: false; code: string };

// Preserve the decimal tokens before JSON.parse rounds integers beyond 2^53.
function exactMoneyJSON(text: string): unknown {
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

async function request<T>(path: string, init?: RequestInit): Promise<Result<T>> {
  try {
    const response = await fetch(new Request(new URL(`/api${path}`, window.location.origin), init));
    const body = exactMoneyJSON(await response.text()) as T & { error?: string };
    return response.ok ? { ok: true, data: body } : { ok: false, code: body.error ?? "server_error" };
  } catch { return { ok: false, code: "network" }; }
}
export const financeEntries = () => request<Entry[]>("/finance/entries");
export const financeBalance = () => request<{ balanceCents: string }>("/finance/balance");
export function createFinanceEntry(input: { categoryId: string; direction: string; date: string; description: string; amountCents: string }) {
  const { amountCents, ...rest } = input;
  if (!/^[1-9]\d{0,18}$/.test(amountCents) || BigInt(amountCents) > 9223372036854775807n) {
    return Promise.resolve({ ok: false as const, code: "invalid_amount" });
  }
  const body = `${JSON.stringify(rest).slice(0, -1)},"amountCents":${amountCents}}`;
  return request<Entry>("/finance/entries", { method: "POST", headers: { "Content-Type": "application/json" }, body });
}
