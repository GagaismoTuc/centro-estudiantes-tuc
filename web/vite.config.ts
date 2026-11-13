import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  server: {
    // The Go API serves everything under /api, so no path rewrite is needed.
    proxy: {
      "/api": "http://localhost:8080",
    },
    // web/ plus the public API contract, which the error-message test reads.
    // Nothing else outside web/ (api/, env files) is reachable from the dev server.
    fs: { allow: [".", "../openapi"] },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
  },
});
