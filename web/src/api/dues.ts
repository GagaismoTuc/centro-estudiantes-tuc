import { moneyRequest, sendMoneyJSON } from "./money.ts";
import type { components } from "./schema.gen.ts";

export type DueSummary = Omit<components["schemas"]["FinanceDueSummary"], "amountCents"> & { amountCents: string };
export type DueDetail = Omit<components["schemas"]["FinanceDueDetail"], "amountCents"> & { amountCents: string };
export type MyDue = Omit<components["schemas"]["MyFinanceDue"], "amountCents"> & { amountCents: string };

export const listDues = () => moneyRequest<DueSummary[]>("/finance/dues");
export const getDue = (id: string) => moneyRequest<DueDetail>(`/finance/dues/${encodeURIComponent(id)}`);
export const listMyDues = () => moneyRequest<MyDue[]>("/finance/dues/mine");

export function createDue(input: { name: string; amountCents: string; dueDate: string }) {
  const { amountCents, ...rest } = input;
  return sendMoneyJSON<DueDetail>("/finance/dues", "POST", amountCents, rest);
}

export function updateDue(id: string, input: { name: string; amountCents: string; dueDate: string }) {
  const { amountCents, ...rest } = input;
  return sendMoneyJSON<DueDetail>(`/finance/dues/${encodeURIComponent(id)}`, "PATCH", amountCents, rest);
}

export const deleteDue = (id: string) =>
  moneyRequest<undefined>(`/finance/dues/${encodeURIComponent(id)}`, { method: "DELETE" });
