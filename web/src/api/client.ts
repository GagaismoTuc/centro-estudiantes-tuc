import createClient from "openapi-fetch";
import type { paths } from "./schema.gen.ts";

// Typed client for the Go API. Types come from openapi/openapi.yaml (`pnpm gen:api`).
// In development Vite proxies /api to the Go server; in production the Go binary serves both.
export const apiClient = createClient<paths>({ baseUrl: "/api" });
