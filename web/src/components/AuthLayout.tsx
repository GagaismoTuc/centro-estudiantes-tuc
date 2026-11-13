import type { ReactNode } from "react";
import { BrandMark } from "./Page.tsx";

/** Signed-out and two-factor screens: a centered card on the neutral page. */
export function AuthLayout({ children, footer }: { children: ReactNode; footer?: ReactNode }) {
  return (
    <div className="auth">
      <main className="auth__main">
        <div className="auth__brand">
          <BrandMark />
          <span>Centro de Estudiantes</span>
        </div>
        <div className="auth__card">{children}</div>
        {footer && <div className="auth__footer">{footer}</div>}
      </main>
    </div>
  );
}
