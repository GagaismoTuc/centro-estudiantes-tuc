import { isValidInt64Cents, moneyRequest } from "./money.ts";
import type { components } from "./schema.gen.ts";

export type DueSummary = Omit<components["schemas"]["FinanceDueSummary"], "amountCents"> & { amountCents: string };
export type DueDetail = Omit<components["schemas"]["FinanceDueDetail"], "amountCents"> & { amountCents: string };
export type MyDue = Omit<components["schemas"]["MyFinanceDue"], "amountCents"> & { amountCents: string };

export const listDues = () => moneyRequest<DueSummary[]>("/finance/dues");
export const getDue = (id: string) => moneyRequest<DueDetail>(`/finance/dues/${encodeURIComponent(id)}`);
export const listMyDues = () => moneyRequest<MyDue[]>("/finance/dues/mine");

export function createDue(input: { name: string; amountCents: string; dueDate: string }) {
  const { amountCents, ...rest } = input;
  if (!isValidInt64Cents(amountCents)) {
    return Promise.resolve({ ok: false as const, code: "invalid_amount" });
  }
  const body = `${JSON.stringify(rest).slice(0, -1)},"amountCents":${amountCents}}`;
  return moneyRequest<DueDetail>("/finance/dues", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body,
  });
}

export function updateDue(id: string, input: { name: string; amountCents: string; dueDate: string }) {
  const { amountCents, ...rest } = input;
  if (!isValidInt64Cents(amountCents)) {
    return Promise.resolve({ ok: false as const, code: "invalid_amount" });
  }
  const body = `${JSON.stringify(rest).slice(0, -1)},"amountCents":${amountCents}}`;
  return moneyRequest<DueDetail>(`/finance/dues/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body,
  });
}

export const deleteDue = (id: string) =>
  moneyRequest<undefined>(`/finance/dues/${encodeURIComponent(id)}`, { method: "DELETE" });
