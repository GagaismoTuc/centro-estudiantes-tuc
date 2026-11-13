import { useEffect, useId, useState } from "react";
import { createPortal } from "react-dom";
import type { components } from "../api/schema.gen.ts";
import { useMe } from "../auth/session.tsx";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { CopyButton } from "../components/CopyButton.tsx";
import { Icon } from "../components/Icon.tsx";
import { neutralizeFormula } from "../lib/csv.ts";
import { guardLeave } from "../lib/router.tsx";

export type IssuedMemberCode = components["schemas"]["IssuedMemberCode"];

const LEAVE = "Los códigos generados no se van a volver a mostrar. ¿Salir igual?";

const dateFormat = new Intl.DateTimeFormat("es-AR", { dateStyle: "long", timeZone: "America/Argentina/Tucuman" });
const formatDate = (iso: string) => dateFormat.format(new Date(iso));

/** Asks before the page is closed, reloaded or left through the app while codes are on screen. */
function useLeaveGuard() {
  useEffect(() => {
    const onBeforeUnload = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener("beforeunload", onBeforeUnload);
    const release = guardLeave(LEAVE);
    return () => {
      window.removeEventListener("beforeunload", onBeforeUnload);
      release();
    };
  }, []);
}

/**
 * Codes exist in plain text only in this response (the API stores an HMAC), so they live
 * in memory until the admin hides them or leaves. Mounted only while there are codes.
 */
export function IssuedCodes({ codes, onHide }: { codes: IssuedMemberCode[]; onHide: () => void }) {
  useLeaveGuard();
  const id = useId();
  const [confirmHide, setConfirmHide] = useState(false);

  return (
    <section className="panel issued" aria-labelledby={`${id}-title`}>
      <div className="panel__header">
        <h2 id={`${id}-title`}>Códigos generados</h2>
        <div className="panel__actions">
          <button type="button" className="button button--secondary" onClick={() => window.print()}>
            <Icon name="print" />
            Imprimir
          </button>
          <button type="button" className="button button--ghost" onClick={() => setConfirmHide(true)}>
            Ocultar códigos
          </button>
        </div>
      </div>
      <p className="issued__warning">
        <Icon name="alert" />
        <span>
          No se van a volver a mostrar: copialos o imprimilos ahora y entregáselos a cada alumno. Si alguien pierde el
          suyo, generale uno nuevo.
        </span>
      </p>
      <div className="table-wrap" tabIndex={0} role="region" aria-labelledby={`${id}-caption`}>
        <table className="data-table">
          <caption id={`${id}-caption`} className="visually-hidden">
            Códigos para entregar
          </caption>
          <thead>
            <tr>
              <th scope="col">Alumno</th>
              <th scope="col">DNI</th>
              <th scope="col">Código</th>
              <th scope="col">Vence</th>
              <th scope="col">
                <span className="visually-hidden">Acciones</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {codes.map((c) => (
              <tr key={c.memberId}>
                <th scope="row">
                  <bdi>{c.fullName}</bdi>
                </th>
                <td data-label="DNI" className="mono">
                  {c.documentNumber}
                </td>
                <td data-label="Código">
                  <code className="access-code">{c.code}</code>
                </td>
                <td data-label="Vence">{formatDate(c.expiresAt)}</td>
                <td>
                  <CopyButton text={c.code} label="Copiar" ariaLabel={`Copiar código de ${c.fullName}`} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {confirmHide && (
        <ConfirmDialog
          title="¿Ocultar los códigos?"
          confirmLabel="Ocultar"
          onConfirm={onHide}
          onCancel={() => setConfirmHide(false)}
        >
          <p>No se van a volver a mostrar. A quien no tenga el suyo vas a tener que generarle uno nuevo.</p>
        </ConfirmDialog>
      )}
      {createPortal(<PrintSheet codes={codes} />, document.body)}
    </section>
  );
}

/** One cut-out card per student; only visible when printing (no app chrome). */
function PrintSheet({ codes }: { codes: IssuedMemberCode[] }) {
  const { tenantName } = useMe();
  const claimUrl = `${window.location.origin}/alta`;
  return (
    <div className="print-sheet" hidden>
      {codes.map((c) => (
        <article className="print-card" key={c.memberId}>
          <p className="print-card__brand">{tenantName !== null ? <bdi>{tenantName}</bdi> : "Centro de Estudiantes"}</p>
          <p className="print-card__name">
            <bdi>{neutralizeFormula(c.fullName)}</bdi>
          </p>
          <p className="print-card__label">Tu código de acceso</p>
          <p className="print-card__code">{c.code}</p>
          <p>Vence el {formatDate(c.expiresAt)}. Sirve una sola vez.</p>
          <p>
            Creá tu cuenta en <span className="print-card__url">{claimUrl}</span>
          </p>
        </article>
      ))}
    </div>
  );
}
