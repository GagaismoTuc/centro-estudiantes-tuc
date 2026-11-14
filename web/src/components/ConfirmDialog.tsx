import { useId, useLayoutEffect, useRef, type ReactNode } from "react";

type Props = {
  title: string;
  children: ReactNode;
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
};

/**
 * A blocking confirmation (Modern Web Guidance `dialog`, rule 1): native modal <dialog>
 * named by its heading. Cancel comes first so it is the initial focus; Esc cancels.
 * The parent mounts it to ask and unmounts it to close.
 *
 * Only the user's own answers (the buttons, Esc) reach the parent. The `close` event is
 * ignored on purpose: it arrives a task after close(), so the close() run by the effect
 * cleanup (StrictMode mounts, cleans up and mounts again) used to land after the second
 * showModal() and read as a cancel, closing the dialog by itself.
 */
export function ConfirmDialog({ title, children, confirmLabel, onConfirm, onCancel }: Props) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();

  useLayoutEffect(() => {
    const dialog = ref.current;
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", ""); // engines without showModal (jsdom)
    // Closing (not just removing) it lets the browser return focus to the invoker.
    return () => {
      if (dialog.open && typeof dialog.close === "function") dialog.close();
    };
  }, []);

  return (
    <dialog
      ref={ref}
      className="confirm"
      aria-labelledby={titleId}
      onCancel={(event) => {
        // Esc: we close by unmounting. If the browser refuses to let us keep it open
        // (a cancel without user activation is not cancelable), it closes anyway.
        event.preventDefault();
        onCancel();
      }}
    >
      <h2 id={titleId}>{title}</h2>
      <div className="confirm__body">{children}</div>
      <div className="confirm__actions">
        <button type="button" className="button button--secondary" onClick={onCancel}>
          Cancelar
        </button>
        <button type="button" className="button button--primary" onClick={onConfirm}>
          {confirmLabel}
        </button>
      </div>
    </dialog>
  );
}
