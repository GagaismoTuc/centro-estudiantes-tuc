import { useEffect, useRef, useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent } from "react";
import { createPortal } from "react-dom";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";

// A tiny pathname router: history.pushState + popstate, no dependencies.

const listeners = new Set<() => void>();
let navigated = false;
// The path the app is showing; on popstate the address bar has already moved away from it.
let shownPath = window.location.pathname;

function notify() {
  navigated = true;
  shownPath = window.location.pathname;
  for (const listener of listeners) listener();
}

// Back/Forward out of a guarded screen: put the screen's entry back and ask first.
// Confirming steps back onto the entry the user was heading to (it sits right before
// the one we pushed, whether they pressed Back or Forward).
let passNextPop = false;
function onPopState() {
  const leaving = window.location.pathname !== shownPath;
  if (passNextPop || !leaving || leaveMessage === null) {
    passNextPop = false;
    notify();
    return;
  }
  window.history.pushState(null, "", shownPath);
  askLeave(
    () => {
      passNextPop = true;
      window.history.back();
    },
    { focusHeadingOnCancel: true },
  );
}

function subscribe(listener: () => void) {
  if (listeners.size === 0) shownPath = window.location.pathname;
  listeners.add(listener);
  window.addEventListener("popstate", onPopState);
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) window.removeEventListener("popstate", onPopState);
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
// the user leaves it through the app or the Back button. Programmatic redirects are not
// asked. Closing or reloading the tab can only use the browser's own prompt
// (beforeunload, in the screen).
let leaveMessage: string | null = null;

/** Registers the question to ask before leaving the current screen; returns the cleanup. */
export function guardLeave(message: string): () => void {
  leaveMessage = message;
  return () => {
    if (leaveMessage === message) leaveMessage = null;
  };
}

// The pending question lives here, not in the control that asked: a link in the phone
// menu unmounts as soon as the menu closes, and the question must outlive it.
type Asking = { message: string; leave: () => void; focusHeadingOnCancel?: boolean };
let asking: Asking | null = null;
const askingListeners = new Set<() => void>();

function setAsking(next: Asking | null) {
  asking = next;
  for (const listener of askingListeners) listener();
}

function subscribeAsking(listener: () => void) {
  askingListeners.add(listener);
  return () => {
    askingListeners.delete(listener);
  };
}

function askLeave(leave: () => void, options?: { focusHeadingOnCancel?: boolean }): void {
  if (leaveMessage === null) leave();
  else setAsking({ message: leaveMessage, leave, ...options });
}

/**
 * Runs `leave` right away when there is nothing to lose; otherwise asks first in the
 * in-app dialog shown by <LeaveConfirmHost>.
 */
export function confirmLeave(leave: () => void): void {
  askLeave(leave);
}

/**
 * The one leave dialog (Modern Web Guidance `dialog`). Mount it once in a part of the
 * page that stays while the user moves around (the app shell).
 */
export function LeaveConfirmHost() {
  const current = useSyncExternalStore(subscribeAsking, () => asking);
  const focusHeadingOnClose = useRef(false);
  // Leaving the shell (signed out, another page) drops a question nobody can see.
  useEffect(() => () => setAsking(null), []);
  // Wait until ConfirmDialog unmounts and calls dialog.close() so the screen is no longer inert.
  useEffect(() => {
    if (!current && focusHeadingOnClose.current) {
      focusHeadingOnClose.current = false;
      document.querySelector<HTMLElement>("main h1, h1")?.focus();
    }
  }, [current]);
  if (!current) return null;
  // A portal: the host may sit inside markup where a dialog cannot.
  return createPortal(
    <ConfirmDialog
      title="¿Salir de esta pantalla?"
      confirmLabel="Salir igual"
      onCancel={() => {
        if (current.focusHeadingOnCancel) focusHeadingOnClose.current = true;
        setAsking(null);
      }}
      onConfirm={() => {
        focusHeadingOnClose.current = false;
        setAsking(null);
        current.leave();
      }}
    >
      <p>{current.message}</p>
    </ConfirmDialog>,
    document.body,
  );
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
    if (!active) confirmLeave(() => navigate(to));
  }
  return <a href={to} aria-current={active ? "page" : undefined} onClick={handleClick} {...rest} />;
}

export function Redirect({ to }: { to: string }) {
  useEffect(() => navigate(to, { replace: true }), [to]);
  return null;
}
