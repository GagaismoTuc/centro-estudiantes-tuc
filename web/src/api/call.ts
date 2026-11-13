// Turns an openapi-fetch call into ok/error with the API's `error` code.
// Network failures become "network"; error bodies without a code become "server_error".

/** On failure `body` is the parsed error body, for errors that carry details (import rows). */
export type Result<T> = { ok: true; data: T } | { ok: false; code: string; body?: unknown };

type FetchResult = { data?: unknown; error?: unknown; response: Response };

export async function call<R extends FetchResult>(request: Promise<R>): Promise<Result<Exclude<R["data"], undefined>>> {
  try {
    const { data, error, response } = await request;
    if (response.ok) return { ok: true, data: data as Exclude<R["data"], undefined> };
    const code =
      typeof error === "object" && error !== null && "error" in error && typeof error.error === "string"
        ? error.error
        : "server_error";
    return { ok: false, code, body: error };
  } catch {
    return { ok: false, code: "network" };
  }
}
