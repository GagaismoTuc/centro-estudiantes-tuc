const LINK_NEXT_KEY = "auth:next";

/**
 * Accepts only same-origin internal paths: must start with `/`, never `//` or `/\`,
 * and never contain backslashes, control characters, or a URL scheme. Anything else
 * falls back to `/`.
 */
export function safeNext(raw: string | null | undefined): string {
  if (!raw) return "/";
  if (!raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return "/";
  if (/[\u0000-\u001f\u007f\\]/.test(raw)) return "/";
  try {
    const base = "https://app.invalid";
    const url = new URL(raw, base);
    if (url.origin !== base) return "/";
    if (url.pathname === "/ingresar") return "/";
    return url.pathname + url.search + url.hash;
  } catch {
    return "/";
  }
}

/** Reads and validates the `next` query parameter from `search`. */
export function readNext(search: string): string {
  return safeNext(new URLSearchParams(search).get("next"));
}

/** Appends `?next=<route>` to `path` unless `next` resolves to `/`. */
export function withNext(path: string, next: string): string {
  const safe = safeNext(next);
  if (safe === "/") return path;
  return `${path}?${new URLSearchParams({ next: safe }).toString()}`;
}

/** Resolves the post-sign-in destination given `home` (`/` or an MFA screen) and `next`. */
export function destinationFor(home: string, next: string): string {
  return home === "/" ? safeNext(next) : withNext(home, next);
}

/** Stores `next` for an emailed sign-in link in this browser tab, or clears it for `/`. */
export function saveLinkNext(next: string): void {
  const safe = safeNext(next);
  try {
    if (safe === "/") sessionStorage.removeItem(LINK_NEXT_KEY);
    else sessionStorage.setItem(LINK_NEXT_KEY, safe);
  } catch {
    // Storage may be unavailable in restricted contexts; fall back to "/" after sign-in.
  }
}

/** Reads and removes the stored `next` path for an emailed sign-in link. */
export function takeLinkNext(): string {
  try {
    const raw = sessionStorage.getItem(LINK_NEXT_KEY);
    sessionStorage.removeItem(LINK_NEXT_KEY);
    return safeNext(raw);
  } catch {
    return "/";
  }
}
