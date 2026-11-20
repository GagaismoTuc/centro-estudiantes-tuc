import { call } from "./call.ts";
import { apiClient } from "./client.ts";
import { moneyRequest, sendMoneyJSON } from "./money.ts";
import type { components } from "./schema.gen.ts";

type Entry = Omit<components["schemas"]["FinanceEntry"], "amountCents"> & { amountCents: string };
export type FinanceReceipt = components["schemas"]["FinanceReceipt"];

export const financeEntries = () => moneyRequest<Entry[]>("/finance/entries");
export const financeBalance = () => moneyRequest<{ balanceCents: string }>("/finance/balance");

export function createFinanceEntry(input: {
  categoryId: string;
  direction: string;
  date: string;
  description: string;
  amountCents: string;
  eventBudgetId?: string;
  headingId?: string;
}) {
  const { amountCents, ...rest } = input;
  return sendMoneyJSON<Entry>("/finance/entries", "POST", amountCents, rest);
}

export const entryReceipts = (entryId: string) =>
  call(apiClient.GET("/finance/entries/{entryId}/receipts", { params: { path: { entryId } } }));

export async function uploadEntryReceipt(entryId: string, file: File) {
  const bytes = new Uint8Array(await file.arrayBuffer());
  return call(
    apiClient.POST("/finance/entries/{entryId}/receipts", {
      params: { path: { entryId } },
      body: "" as never,
      bodySerializer: () => bytes,
      headers: { "Content-Type": "application/octet-stream" },
    }),
  );
}
