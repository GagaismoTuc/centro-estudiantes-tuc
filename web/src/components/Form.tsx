import { useId, useState, type InputHTMLAttributes, type ReactNode, type Ref } from "react";
import { Icon } from "./Icon.tsx";

export type Status = { tone: "error" | "ok" | "info"; text: string } | null;

/**
 * The form's polite live region. It stays mounted and only its text changes, so
 * screen readers announce server errors and confirmations reliably.
 */
export function FormStatus({ status }: { status: Status }) {
  return (
    <div className="form-status" data-tone={status?.tone} aria-live="polite" aria-atomic="true">
      {status && (
        <p>
          <Icon name={status.tone === "error" ? "alert" : "check"} />
          <span>{status.text}</span>
        </p>
      )}
    </div>
  );
}

type FieldProps = Omit<InputHTMLAttributes<HTMLInputElement>, "id"> & {
  label: string;
  /** Format help, shown above the input so autofill popovers and keyboards never cover it. */
  hint?: ReactNode;
  name: string;
  ref?: Ref<HTMLInputElement>;
};

export function Field({ label, hint, name, ...input }: FieldProps) {
  const id = useId();
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {hint && (
        <p className="field__hint" id={`${id}-hint`}>
          {hint}
        </p>
      )}
      <input id={id} name={name} aria-describedby={hint ? `${id}-hint` : undefined} {...input} />
    </div>
  );
}

/** A password input with a "Mostrar" toggle; paste and password managers keep working. */
export function PasswordField({ label, hint, name, ...input }: FieldProps) {
  const id = useId();
  const [shown, setShown] = useState(false);
  return (
    <div className="field">
      <div className="field__row">
        <label htmlFor={id}>{label}</label>
        <button
          type="button"
          className="text-button"
          aria-pressed={shown}
          aria-controls={id}
          onClick={() => setShown((s) => !s)}
        >
          {shown ? "Ocultar" : "Mostrar"}
        </button>
      </div>
      {hint && (
        <p className="field__hint" id={`${id}-hint`}>
          {hint}
        </p>
      )}
      <input
        id={id}
        name={name}
        type={shown ? "text" : "password"}
        aria-describedby={hint ? `${id}-hint` : undefined}
        {...input}
      />
    </div>
  );
}

export function SubmitButton({ children, pending }: { children: ReactNode; pending: boolean }) {
  return (
    <button type="submit" className="button button--primary button--block" disabled={pending} aria-busy={pending || undefined}>
      {children}
    </button>
  );
}
