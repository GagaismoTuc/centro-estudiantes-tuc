import { vi } from "vitest";
import type { components } from "../api/schema.gen.ts";

export type Me = components["schemas"]["Me"];

/** A canned API answer; built into a fresh Response on every call. */
export type Reply = { status: number; body?: unknown };

/** A request the app sent, as seen at the fetch boundary. */
export type Sent = { method: string; path: string; body: unknown; href: string };

export const ok = (body?: unknown, status = 200): Reply => ({ status, body });
export const noContent = (status = 204): Reply => ({ status });
export const fail = (status: number, error: string): Reply => ({ status, body: { error } });

export function me(overrides: Partial<Me> = {}): Me {
  return {
    userId: "u1",
    email: "ana@escuela.edu.ar",
    emailVerified: true,
    tenantId: "t1",
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
    sent.push({ method: request.method, path, body: text ? JSON.parse(text) : undefined, href: window.location.href });

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
