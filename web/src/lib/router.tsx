import { useEffect, useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent } from "react";

// A tiny pathname router: history.pushState + popstate, no dependencies.

const listeners = new Set<() => void>();
let navigated = false;

function notify() {
  navigated = true;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  window.addEventListener("popstate", notify);
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) window.removeEventListener("popstate", notify);
  };
}

export function navigate(to: string, options: { replace?: boolean } = {}) {
  if (options.replace) window.history.replaceState(null, "", to);
  else window.history.pushState(null, "", to);
  notify();
}

export function usePathname(): string {
  return useSyncExternalStore(subscribe, () => window.location.pathname);
}

// A screen holding something that cannot be recovered (issued access codes) asks before
// the user leaves it through the app. Programmatic redirects are not asked.
let leaveMessage: string | null = null;

/** Registers the question to ask before leaving the current screen; returns the cleanup. */
export function guardLeave(message: string): () => void {
  leaveMessage = message;
  return () => {
    if (leaveMessage === message) leaveMessage = null;
  };
}

/** True when there is nothing to lose or the user agreed to leave. */
export function confirmLeave(): boolean {
  return leaveMessage === null || window.confirm(leaveMessage);
}

/** True once the user moved between routes in this page load (used to move focus to the new heading). */
export function hasNavigated(): boolean {
  return navigated;
}

type LinkProps = Omit<AnchorHTMLAttributes<HTMLAnchorElement>, "href"> & { to: string };

export function Link({ to, onClick, ...rest }: LinkProps) {
  const active = usePathname() === to;
  function handleClick(event: MouseEvent<HTMLAnchorElement>) {
    onClick?.(event);
    // Let the browser handle new tabs, downloads and modified clicks.
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || rest.target) {
      return;
    }
    event.preventDefault();
    if (!active && confirmLeave()) navigate(to);
  }
  return <a href={to} aria-current={active ? "page" : undefined} onClick={handleClick} {...rest} />;
}

export function Redirect({ to }: { to: string }) {
  useEffect(() => navigate(to, { replace: true }), [to]);
  return null;
}
