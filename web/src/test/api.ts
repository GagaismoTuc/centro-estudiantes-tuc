import { render } from "@testing-library/react";
import { createElement, StrictMode, type ReactNode } from "react";
import { vi } from "vitest";
import type { components } from "../api/schema.gen.ts";

export type Me = components["schemas"]["Me"];

/** A canned API answer; built into a fresh Response on every call. */
export type Reply = { status: number; body?: unknown };

/** A request the app sent, as seen at the fetch boundary. */
export type Sent = { method: string; path: string; body: unknown; contentType: string | null; href: string };

export const ok = (body?: unknown, status = 200): Reply => ({ status, body });
export const noContent = (status = 204): Reply => ({ status });
export const fail = (status: number, error: string): Reply => ({ status, body: { error } });

export function me(overrides: Partial<Me> = {}): Me {
  return {
    userId: "u1",
    email: "ana@escuela.edu.ar",
    emailVerified: true,
    tenantId: "t1",
    tenantName: "Escuela Demo Tucumán",
    role: "student",
    mfa: "not_required",
    ...overrides,
  };
}

/**
 * Routes "METHOD /path" (path without the /api prefix) to replies. An array is
 * answered in order and its last entry repeats. Unrouted GET /auth/me answers
 * 401 (signed out); any other unrouted request fails the test loudly with 599.
 * Returns the list of requests sent, in order.
 */
export function mockApi(routes: Record<string, Reply | Reply[]>): Sent[] {
  const sent: Sent[] = [];
  const calls = new Map<string, number>();
  vi.mocked(fetch).mockImplementation(async (input) => {
    const request = input as Request;
    const url = new URL(request.url);
    const path = url.pathname.replace(/^\/api/, "");
    const key = `${request.method} ${path}`;
    const text = await request.text();
    const contentType = request.headers.get("Content-Type");
    // JSON bodies are parsed; anything else (CSV uploads) is kept as the raw text.
    const body = !text ? undefined : contentType?.includes("json") ? JSON.parse(text) : text;
    sent.push({ method: request.method, path, body, contentType, href: window.location.href });

    const route = routes[key];
    let reply: Reply;
    if (route === undefined) {
      reply = key === "GET /auth/me" ? fail(401, "unauthenticated") : fail(599, `unrouted ${key}`);
    } else if (Array.isArray(route)) {
      const n = calls.get(key) ?? 0;
      calls.set(key, n + 1);
      reply = route[Math.min(n, route.length - 1)] as Reply;
    } else {
      reply = route;
    }
    if (reply.body === undefined) return new Response(null, { status: reply.status });
    return new Response(JSON.stringify(reply.body), {
      status: reply.status,
      headers: { "Content-Type": "application/json" },
    });
  });
  return sent;
}

/** Requests matching "METHOD /path". */
export function sentTo(sent: Sent[], key: string): Sent[] {
  return sent.filter((s) => `${s.method} ${s.path}` === key);
}

/**
 * Renders inside <StrictMode>, as main.tsx does: effects run mount → cleanup → mount,
 * which is how the leave dialog closed itself in the browser.
 */
export function renderStrict(ui: ReactNode) {
  return render(createElement(StrictMode, null, ui));
}

/**
 * Gives jsdom a browser-like <dialog>: showModal()/close() toggle `open`, and close()
 * delivers its `close` event in a later task, as browsers do. Returns the restore.
 */
export function emulateDialog(): () => void {
  const proto = HTMLDialogElement.prototype as unknown as Record<string, unknown>;
  const saved = { showModal: proto.showModal, show: proto.show, close: proto.close };
  function open(this: HTMLDialogElement) {
    if (!this.hasAttribute("open")) this.setAttribute("open", "");
  }
  proto.showModal = open;
  proto.show = open;
  proto.close = function close(this: HTMLDialogElement) {
    if (!this.hasAttribute("open")) return;
    this.removeAttribute("open");
    setTimeout(() => this.dispatchEvent(new Event("close")));
  };
  return () => Object.assign(proto, saved);
}

/** Esc on an open modal dialog: a cancelable `cancel`, then close() unless prevented. */
export function pressEscape(dialog: HTMLElement) {
  const event = new Event("cancel", { cancelable: true });
  dialog.dispatchEvent(event);
  if (!event.defaultPrevented) (dialog as HTMLDialogElement).close();
}

/** Lets queued tasks (dialog close events, history traversal) run. */
export const settle = (ms = 50) => new Promise((resolve) => setTimeout(resolve, ms));
