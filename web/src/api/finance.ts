import { moneyRequest, sendMoneyJSON } from "./money.ts";
import type { components } from "./schema.gen.ts";

type Entry = Omit<components["schemas"]["FinanceEntry"], "amountCents"> & { amountCents: string };

export const financeEntries = () => moneyRequest<Entry[]>("/finance/entries");
export const financeBalance = () => moneyRequest<{ balanceCents: string }>("/finance/balance");
export function createFinanceEntry(input: { categoryId: string; direction: string; date: string; description: string; amountCents: string }) {
  const { amountCents, ...rest } = input;
  return sendMoneyJSON<Entry>("/finance/entries", "POST", amountCents, rest);
}
