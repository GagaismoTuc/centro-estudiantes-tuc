import {
  useEffect,
  useId,
  useRef,
  useState,
  type FormEvent,
  type InputHTMLAttributes,
  type ReactNode,
  type Ref,
  type RefObject,
} from "react";
import { errorFields } from "../api/errors.ts";
import { Icon } from "./Icon.tsx";

export type Status = { tone: "error" | "ok" | "info"; text: string } | null;

/**
 * The form's polite live region. It stays mounted and only its text changes, so
 * screen readers announce server errors and confirmations reliably. It can take
 * focus (tabIndex -1) when an error is not about any one field.
 */
export function FormStatus({ status, id, ref }: { status: Status; id?: string; ref?: Ref<HTMLDivElement> }) {
  return (
    <div ref={ref} id={id} tabIndex={-1} className="form-status" data-tone={status?.tone} aria-live="polite" aria-atomic="true">
      {status && (
        <p>
          <Icon name={status.tone === "error" ? "alert" : "check"} />
          <span>{status.text}</span>
        </p>
      )}
    </div>
  );
}

// ---------- Validation (Modern Web Guidance `forms`, `accessible-error-announcement`) ----------
// Forms are noValidate: the browser's bubbles are in the browser's language and vanish.
// We check on submit, show a Spanish message under each field, mark it aria-invalid and
// move focus to the first invalid one.

/** Returns the Spanish message for an invalid value, or null. Checkboxes get "on" or "". */
export type Check = (value: string) => string | null;

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export const checks = {
  email: (v: string) =>
    !v.trim() ? "Escribí tu mail." : EMAIL.test(v.trim()) ? null : "Revisá el mail: tiene que ser como nombre@dominio.com.",
  password: (v: string) => (v ? null : "Escribí tu contraseña."),
  newPassword: (v: string) =>
    !v ? "Escribí una contraseña." : v.length < 10 || v.length > 128 ? "La contraseña tiene que tener entre 10 y 128 caracteres." : null,
  accessCode: (v: string) =>
    !v
      ? "Escribí el código de acceso."
      : /^[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}$/.test(v)
        ? null
        : "El código tiene 12 letras y números, por ejemplo ABCD-EFGH-JKMN.",
  otp: (v: string) => (!v.trim() ? "Escribí el código de 6 dígitos." : /^\d{6}$/.test(v.trim()) ? null : "El código tiene 6 números."),
  recoveryCode: (v: string) => (v.trim() ? null : "Escribí uno de tus códigos de recuperación."),
} satisfies Record<string, Check>;

/** What a field needs to show its error: spread into Field / PasswordField. */
export type FieldState = { name: string; error?: string | undefined; invalidBy?: string | undefined };

/**
 * Validation and error focus for one form. Spread `formProps` on the <form>, `field(name)`
 * on each field and `statusProps` on its FormStatus.
 */
export function useFormErrors() {
  const formRef = useRef<HTMLFormElement>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const statusId = useId();
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [related, setRelated] = useState<string[]>([]);
  // Focus moves after the render that shows the message, so it is read with it.
  const focusNext = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!focusNext.current) return;
    focusNext.current.focus();
    focusNext.current = null;
  });

  function inputs(names: string[]): HTMLInputElement[] {
    const form = formRef.current;
    if (!form) return [];
    return Array.from(form.elements).filter(
      (el): el is HTMLInputElement => el instanceof HTMLInputElement && names.includes(el.name),
    );
  }

  /** Checks the named fields; shows their messages and focuses the first invalid one. */
  function validate(fieldChecks: Record<string, Check>): boolean {
    const found: Record<string, string> = {};
    let first: HTMLInputElement | null = null;
    for (const input of inputs(Object.keys(fieldChecks))) {
      const message = fieldChecks[input.name]?.(input.type === "checkbox" ? (input.checked ? "on" : "") : input.value);
      if (message) {
        found[input.name] = message;
        first ??= input;
      }
    }
    setErrors(found);
    setRelated([]);
    focusNext.current = first;
    if (first) first.focus(); // also now: never leave focus on a disabled button or BODY
    return first === null;
  }

  /** After a server error: marks the fields it is about and focuses the first, or the message. */
  function failed(code: string) {
    const fields = inputs(errorFields(code));
    setErrors({});
    setRelated(fields.map((f) => f.name));
    focusNext.current = fields[0] ?? statusRef.current;
  }

  /** Clears the marks once the user edits the field. */
  function onChange(event: FormEvent<HTMLFormElement>) {
    const name = (event.target as HTMLInputElement).name;
    if (name in errors) {
      setErrors((current) => {
        const rest = { ...current };
        delete rest[name];
        return rest;
      });
    }
    if (related.length) setRelated([]);
  }

  return {
    validate,
    failed,
    /** Forgets every mark (for example when the form swaps its fields). */
    clear: () => {
      setErrors({});
      setRelated([]);
    },
    /** The current text of a field (for actions outside the submit, like "send me a link"). */
    value: (name: string) => inputs([name])[0]?.value ?? "",
    field: (name: string): FieldState => ({
      name,
      error: errors[name],
      invalidBy: related.includes(name) ? statusId : undefined,
    }),
    formProps: { ref: formRef as RefObject<HTMLFormElement>, noValidate: true, onChange },
    statusProps: { id: statusId, ref: statusRef },
  };
}

type FieldProps = Omit<InputHTMLAttributes<HTMLInputElement>, "id"> & {
  label: string;
  /** Format help, shown above the input so autofill popovers and keyboards never cover it. */
  hint?: ReactNode;
  name: string;
  ref?: Ref<HTMLInputElement>;
  /** Spanish message shown under the input. */
  error?: string | undefined;
  /** Id of a message elsewhere (the form status) that says why this field is invalid. */
  invalidBy?: string | undefined;
};

/** The inline message under a field. Styled with the form-status error tone. */
export function FieldError({ id, children }: { id: string; children: ReactNode }) {
  return (
    <div className="field__error form-status" data-tone="error" id={id}>
      <p>
        <Icon name="alert" />
        <span>{children}</span>
      </p>
    </div>
  );
}

function describedBy(id: string, hint: unknown, error: unknown, invalidBy: string | undefined) {
  return [hint ? `${id}-hint` : null, error ? `${id}-error` : null, invalidBy ?? null].filter(Boolean).join(" ") || undefined;
}

export function Field({ label, hint, name, error, invalidBy, ...input }: FieldProps) {
  const id = useId();
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {hint && (
        <p className="field__hint" id={`${id}-hint`}>
          {hint}
        </p>
      )}
      <input
        id={id}
        name={name}
        aria-invalid={error || invalidBy ? true : undefined}
        aria-describedby={describedBy(id, hint, error, invalidBy)}
        {...input}
      />
      {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
    </div>
  );
}

/** A password input with a "Mostrar" toggle; paste and password managers keep working. */
export function PasswordField({ label, hint, name, error, invalidBy, ...input }: FieldProps) {
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
        aria-invalid={error || invalidBy ? true : undefined}
        aria-describedby={describedBy(id, hint, error, invalidBy)}
        {...input}
      />
      {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
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
