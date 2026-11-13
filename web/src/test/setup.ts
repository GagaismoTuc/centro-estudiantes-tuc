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

// index.html ships this meta; theme code updates it rather than relying on creating it.
const colorScheme = document.createElement("meta");
colorScheme.name = "color-scheme";
colorScheme.content = "light dark";
document.head.append(colorScheme);

// jsdom has no element scrolling; the navigation drawer scrolls its sheet in and out.
Element.prototype.scrollTo ??= () => {};

afterEach(() => {
  cleanup();
  vi.mocked(fetch).mockReset();
  localStorage.clear();
  document.documentElement.removeAttribute("data-theme");
  colorScheme.content = "light dark";
  window.history.replaceState(null, "", "/");
});
