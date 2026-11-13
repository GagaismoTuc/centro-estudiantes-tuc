import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

// openapi-fetch captures globalThis.fetch when the client module loads,
// so the mock must exist before App (and the client) are imported.
const fetchMock = vi.hoisted(() => {
  const mock = vi.fn<typeof fetch>();
  globalThis.fetch = mock;
  return mock;
});

import { App } from "./App.tsx";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("App API status", () => {
  beforeEach(() => {
    fetchMock.mockReset();
  });

  test("shows a loading state while the health check is pending", () => {
    fetchMock.mockReturnValue(new Promise<Response>(() => {}));
    render(<App />);
    expect(screen.getByText("API: comprobando…")).toBeTruthy();
  });

  test("renders 'API: ok' when GET /api/health succeeds", async () => {
    fetchMock.mockResolvedValue(json({ status: "ok" }));
    render(<App />);
    expect(await screen.findByText("API: ok")).toBeTruthy();
    const request = fetchMock.mock.calls[0]?.[0];
    expect(request).toBeInstanceOf(Request);
    expect(new URL((request as Request).url).pathname).toBe("/api/health");
    expect((request as Request).method).toBe("GET");
  });

  test("renders 'API: sin conexión' when the request fails", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    render(<App />);
    expect(await screen.findByText("API: sin conexión")).toBeTruthy();
  });

  test("renders 'API: sin conexión' when the API answers with an error status", async () => {
    fetchMock.mockResolvedValue(new Response("Bad Gateway", { status: 502 }));
    render(<App />);
    expect(await screen.findByText("API: sin conexión")).toBeTruthy();
  });
});
