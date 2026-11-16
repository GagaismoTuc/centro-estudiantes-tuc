import { useSyncExternalStore } from "react";
import { call, type Result } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import type { components } from "../api/schema.gen.ts";

export type CommissionStatus = components["schemas"]["ElectoralCommissionStatus"];
export type Election = components["schemas"]["Election"];
export type ElectionState = components["schemas"]["ElectionState"];

export const stateLabel: Record<ElectionState, string> = {
  draft: "Borrador",
  submission: "Presentación de listas",
  scheduled: "Programada",
  open: "Abierta",
  closed: "Cerrada",
};

export const DEFAULT_POSITIONS = ["Presidente", "Vicepresidente", "Secretario", "Tesorero"];

// Argentina has no daylight saving time: every date the UI shows or sends is at -03:00.
const OFFSET_MS = 3 * 60 * 60 * 1000;
const argentina = new Intl.DateTimeFormat("es-AR", {
  timeZone: "America/Argentina/Tucuman",
  day: "numeric",
  month: "long",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
});

/** "10 de marzo de 2027 a las 08:00", in Argentina time. */
export function formatArgentina(iso: string): string {
  return argentina.format(new Date(iso));
}

/** RFC 3339 → the value of a datetime-local input, in Argentina time ("2027-03-10T08:00"). */
export function toLocalInput(iso: string): string {
  return new Date(Date.parse(iso) - OFFSET_MS).toISOString().slice(0, 16);
}

/** A datetime-local value read as Argentina time → RFC 3339 with -03:00. */
export function fromLocalInput(value: string): string {
  return `${value.length === 16 ? `${value}:00` : value}-03:00`;
}

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
