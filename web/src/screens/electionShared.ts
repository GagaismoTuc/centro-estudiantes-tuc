import { useSyncExternalStore } from "react";
import { call, type Result } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import type { components } from "../api/schema.gen.ts";

export type CommissionMember = components["schemas"]["CommissionMember"];
export type CommissionStatus = components["schemas"]["ElectoralCommissionStatus"];
export type Election = components["schemas"]["Election"];
export type ElectionState = components["schemas"]["ElectionState"];
export type ElectionList = components["schemas"]["ElectionList"];
export type ElectionListStatus = components["schemas"]["ElectionListStatus"];
export type ElectionListsResponse = components["schemas"]["ElectionListsResponse"];

export const listStatusLabel: Record<ElectionListStatus, string> = {
  pending: "Pendiente",
  approved: "Aprobada",
  rejected: "Rechazada",
};

export const stateLabel: Record<ElectionState, string> = {
  draft: "Borrador",
  submission: "Presentación de listas",
  scheduled: "Programada",
  open: "Abierta",
  closed: "Cerrada",
  annulled: "Anulada",
};

export const DEFAULT_POSITIONS = ["Presidente", "Vicepresidente", "Secretario", "Tesorero"];

// The caller's place on the Electoral commission, shared by the menu and the screens:
// the menu shows "Centro de estudiantes" only to its members and updates after a designation.
let current: CommissionStatus | null = null;
let inflight: Promise<Result<CommissionStatus>> | null = null;
let generation = 0;
const listeners = new Set<() => void>();

function publish(next: CommissionStatus | null) {
  current = next;
  for (const listener of listeners) listener();
}

/** GET /electoral-commission; callers that ask at the same time share one request. */
export function loadCommissionStatus(): Promise<Result<CommissionStatus>> {
  if (inflight) return inflight;
  const mine = generation;
  inflight = call(apiClient.GET("/electoral-commission")).then((result) => {
    if (mine === generation) {
      inflight = null;
      publish(result.ok ? result.data : null);
    }
    return result;
  });
  return inflight;
}

/** Forgets the status (the shell unmounted: signed out or another account). */
export function resetCommissionStatus() {
  generation += 1;
  inflight = null;
  publish(null);
}

export function useCommissionStatus(): CommissionStatus | null {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    () => current,
  );
}
