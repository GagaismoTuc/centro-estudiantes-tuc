import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";

// Node's Request rejects relative URLs such as "/api/health"; browsers resolve
// them against the page. Mirror the browser so the client keeps baseUrl "/api".
const NodeRequest = globalThis.Request;
class BrowserLikeRequest extends NodeRequest {
  constructor(input: RequestInfo | URL, init?: RequestInit) {
    super(typeof input === "string" ? new URL(input, globalThis.location.href) : input, init);
  }
}
globalThis.Request = BrowserLikeRequest;

// openapi-fetch captures globalThis.fetch when the client module loads, so the
// mock must exist before any test file imports the client (setup files run first).
globalThis.fetch = vi.fn<typeof fetch>();

afterEach(() => {
  cleanup();
  vi.mocked(fetch).mockReset();
  localStorage.clear();
  document.documentElement.removeAttribute("data-theme");
  window.history.replaceState(null, "", "/");
});
