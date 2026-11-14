import { useEffect, useState, useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";

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
// the user leaves it through the app. Programmatic redirects are not asked. Closing or
// reloading the tab can only use the browser's own prompt (beforeunload, in the screen).
let leaveMessage: string | null = null;

/** Registers the question to ask before leaving the current screen; returns the cleanup. */
export function guardLeave(message: string): () => void {
  leaveMessage = message;
  return () => {
    if (leaveMessage === message) leaveMessage = null;
  };
}

/**
 * Asks in an in-app dialog (Modern Web Guidance `dialog`) before leaving a guarded screen.
 * `confirmLeave(leave)` runs `leave` right away when there is nothing to lose; render
 * `dialog` next to the control that asked.
 */
export function useLeaveConfirm(): { confirmLeave: (leave: () => void) => void; dialog: ReactNode } {
  const [asking, setAsking] = useState<{ message: string; leave: () => void } | null>(null);

  function confirmLeave(leave: () => void) {
    if (leaveMessage === null) leave();
    else setAsking({ message: leaveMessage, leave });
  }

  // A portal: the asking control may sit inside a <p> or a list, where a dialog cannot.
  const dialog =
    asking &&
    createPortal(
      <ConfirmDialog
        title="¿Salir de esta pantalla?"
        confirmLabel="Salir igual"
        onCancel={() => setAsking(null)}
        onConfirm={() => {
          setAsking(null);
          asking.leave();
        }}
      >
        <p>{asking.message}</p>
      </ConfirmDialog>,
      document.body,
    );
  return { confirmLeave, dialog };
}

/** True once the user moved between routes in this page load (used to move focus to the new heading). */
export function hasNavigated(): boolean {
  return navigated;
}

type LinkProps = Omit<AnchorHTMLAttributes<HTMLAnchorElement>, "href"> & { to: string };

export function Link({ to, onClick, ...rest }: LinkProps) {
  const active = usePathname() === to;
  const { confirmLeave, dialog } = useLeaveConfirm();
  function handleClick(event: MouseEvent<HTMLAnchorElement>) {
    onClick?.(event);
    // Let the browser handle new tabs, downloads and modified clicks.
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || rest.target) {
      return;
    }
    event.preventDefault();
    if (!active) confirmLeave(() => navigate(to));
  }
  return (
    <>
      <a href={to} aria-current={active ? "page" : undefined} onClick={handleClick} {...rest} />
      {dialog}
    </>
  );
}

export function Redirect({ to }: { to: string }) {
  useEffect(() => navigate(to, { replace: true }), [to]);
  return null;
}
