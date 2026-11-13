import { useEffect, useRef, type ReactNode } from "react";
import { hasNavigated } from "../lib/router.tsx";

const SITE = "Centro de Estudiantes";

/** Sets the tab title, front-loaded with the page topic. */
export function useDocumentTitle(title: string) {
  useEffect(() => {
    document.title = `${title} · ${SITE}`;
  }, [title]);
}

/**
 * The page's single h1. After a client-side navigation it takes focus, so keyboard
 * and screen reader users start at the new content instead of the old link.
 */
export function PageTitle({ children, eyebrow }: { children: ReactNode; eyebrow?: string }) {
  const ref = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (hasNavigated()) ref.current?.focus();
  }, []);
  return (
    <div className="page-title">
      {eyebrow && <p className="eyebrow">{eyebrow}</p>}
      <h1 ref={ref} tabIndex={-1}>
        {children}
      </h1>
    </div>
  );
}

export function BrandMark() {
  return (
    <span className="brand-mark" aria-hidden="true">
      CE
    </span>
  );
}
