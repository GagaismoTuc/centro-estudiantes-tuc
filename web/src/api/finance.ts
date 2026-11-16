import { isValidInt64Cents, moneyRequest } from "./money.ts";
import type { components } from "./schema.gen.ts";

type Entry = Omit<components["schemas"]["FinanceEntry"], "amountCents"> & { amountCents: string };

export const financeEntries = () => moneyRequest<Entry[]>("/finance/entries");
export const financeBalance = () => moneyRequest<{ balanceCents: string }>("/finance/balance");
export function createFinanceEntry(input: { categoryId: string; direction: string; date: string; description: string; amountCents: string }) {
  const { amountCents, ...rest } = input;
  if (!isValidInt64Cents(amountCents)) {
    return Promise.resolve({ ok: false as const, code: "invalid_amount" });
  }
  const body = `${JSON.stringify(rest).slice(0, -1)},"amountCents":${amountCents}}`;
  return moneyRequest<Entry>("/finance/entries", { method: "POST", headers: { "Content-Type": "application/json" }, body });
}
