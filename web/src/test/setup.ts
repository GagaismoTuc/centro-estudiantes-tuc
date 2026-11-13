import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Node's Request rejects relative URLs such as "/api/health"; browsers resolve
// them against the page. Mirror the browser so the client keeps baseUrl "/api".
const NodeRequest = globalThis.Request;
class BrowserLikeRequest extends NodeRequest {
  constructor(input: RequestInfo | URL, init?: RequestInit) {
    super(typeof input === "string" ? new URL(input, globalThis.location.href) : input, init);
  }
}
globalThis.Request = BrowserLikeRequest;

afterEach(cleanup);
