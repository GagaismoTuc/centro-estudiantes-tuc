import { moneyRequest, sendMoneyJSON } from "./money.ts";
import type { components } from "./schema.gen.ts";

export type DueSummary = Omit<components["schemas"]["FinanceDueSummary"], "amountCents"> & { amountCents: string };
export type DueMember = Omit<components["schemas"]["FinanceDueMember"], "paidCents"> & { paidCents: string };
export type DueDetail = Omit<components["schemas"]["FinanceDueDetail"], "amountCents" | "members"> & {
  amountCents: string;
  members: DueMember[];
};
export type MyDue = Omit<components["schemas"]["MyFinanceDue"], "amountCents" | "paidCents"> & {
  amountCents: string;
  paidCents: string;
};

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

export function recordDuePayment(dueId: string, input: { rosterMemberId: string; amountCents: string; date: string }) {
  const { amountCents, ...rest } = input;
  return sendMoneyJSON<unknown>(`/finance/dues/${encodeURIComponent(dueId)}/payments`, "POST", amountCents, rest);
}

/** The per-student list of a due as a CSV download; the session cookie authorizes it. */
export const dueMembersCsvHref = (dueId: string) => `/api/finance/dues/${encodeURIComponent(dueId)}/members.csv`;

export const deleteDue = (id: string) =>
  moneyRequest<undefined>(`/finance/dues/${encodeURIComponent(id)}`, { method: "DELETE" });
