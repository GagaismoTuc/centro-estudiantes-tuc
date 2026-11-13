import { useEffect, useRef, useState } from "react";
import { Icon } from "./Icon.tsx";

/**
 * Copies `text` with the Async Clipboard API (Modern Web Guidance `copy-button`).
 * The bubble next to the button is the live region for both outcomes; it is
 * outside the button because a button's children are not reliably announced.
 */
export function CopyButton({ text, label, ariaLabel }: { text: string; label: string; ariaLabel?: string }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  async function copy() {
    window.clearTimeout(timer.current);
    setState("idle");
    let ok = true;
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      ok = false;
    }
    setState(ok ? "copied" : "failed");
    timer.current = window.setTimeout(() => setState("idle"), 2000);
  }

  return (
    <span className="copy">
      <button
        type="button"
        className="button button--secondary"
        aria-label={ariaLabel}
        data-state={state}
        onClick={() => void copy()}
      >
        <Icon name={state === "copied" ? "check" : "copy"} />
        {label}
      </button>
      <span className="copy__tip" role="status" data-visible={state !== "idle" || undefined}>
        {state === "copied" ? "Copiado" : state === "failed" ? "No se pudo copiar" : ""}
      </span>
    </span>
  );
}
