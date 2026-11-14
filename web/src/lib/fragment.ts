import { useEffect, useState } from "react";

// Emailed links carry their single-use token in the URL fragment (`#token=…`), which
// never reaches servers or Referer headers. We take it out of the address bar before
// anything else runs, so it does not stay in history, screenshots or shared URLs.

let captured: { path: string; token: string } | null = null;

/** Moves a `#token=` fragment from the URL into memory. Safe to call on every render. */
export function captureFragmentToken(): void {
  const { hash, pathname, search } = window.location;
  if (!hash) return;
  const token = new URLSearchParams(hash.slice(1)).get("token");
  if (token === null) return;
  window.history.replaceState(window.history.state, "", pathname + search);
  if (token) captured = { path: pathname, token };
}

/** Returns the token captured on `path`, once. */
export function takeFragmentToken(path: string): string | null {
  if (captured?.path !== path) return null;
  const { token } = captured;
  captured = null;
  return token;
}

/**
 * The token captured on `path`, and any newer one: a second emailed link opened while
 * this screen is already showing only changes the fragment (hashchange, no reload).
 */
export function useFragmentToken(path: string): [string | null, (token: string | null) => void] {
  const [token, setToken] = useState(() => takeFragmentToken(path));
  useEffect(() => {
    function onHashChange() {
      captureFragmentToken();
      const next = takeFragmentToken(path);
      if (next) setToken(next);
    }
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, [path]);
  return [token, setToken];
}
