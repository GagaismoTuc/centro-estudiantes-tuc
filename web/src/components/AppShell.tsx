import { useEffect, useRef, useState, type ReactNode } from "react";
import { canReadContacts, hasRole, roleLabels, useMe } from "../auth/session.tsx";
import { LeaveConfirmHost, Link } from "../lib/router.tsx";
import { loadCommissionStatus, resetCommissionStatus, useCommissionStatus } from "../screens/electionShared.ts";
import { Icon, type IconName } from "./Icon.tsx";
import { BrandMark } from "./Page.tsx";
import { SignOutButton } from "./SignOut.tsx";
import { ThemeSwitch } from "./ThemeSwitch.tsx";

const upcoming: { label: string; icon: IconName }[] = [
  { label: "Notas", icon: "note" },
];

function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const me = useMe();
  const commission = useCommissionStatus();
  return (
    <div className="sidebar__inner">
      <div className="sidebar__brand">
        <BrandMark />
        <div>
          <p className="sidebar__title">Centro de Estudiantes</p>
          {me.tenantName !== null && (
            <p className="sidebar__school">
              <bdi>{me.tenantName}</bdi>
            </p>
          )}
          <p className="sidebar__subtitle">{roleLabels(me)}</p>
        </div>
      </div>

      <nav aria-label="Principal" className="nav">
        <p className="eyebrow nav__group" aria-hidden="true">
          Principal
        </p>
        <ul>
          <li>
            <Link to="/" className="nav__item" onClick={onNavigate}>
              <Icon name="home" />
              Inicio
            </Link>
          </li>
          {hasRole(me, "school_admin") && (
            <li>
              <Link to="/padron" className="nav__item" onClick={onNavigate}>
                <Icon name="users" />
                Padrón
              </Link>
            </li>
          )}
          {hasRole(me, "school_admin") && <li><Link to="/miembros" className="nav__item" onClick={onNavigate}><Icon name="users" />Miembros</Link></li>}
          {me.role !== null && (
            <li>
              <Link to="/eleccion" className="nav__item" onClick={onNavigate}>
                <Icon name="ballot" />
                Elección
              </Link>
            </li>
          )}
          {commission?.isMember && (
            <li>
              <Link to="/centro-de-estudiantes" className="nav__item" onClick={onNavigate}>
                <Icon name="shield" />
                Centro de estudiantes
              </Link>
            </li>
          )}
          {hasRole(me, "officer") && (
            <li>
              <Link to="/calculadora" className="nav__item" onClick={onNavigate}>
                <Icon name="calculator" />
                Calculadora
              </Link>
            </li>
          )}
          {canReadContacts(me) && (
            <>
              <li>
                <Link to="/contactos" className="nav__item" onClick={onNavigate}>
                  <Icon name="contacts" />
                  Contactos
                </Link>
              </li>
              <li>
                <Link to="/contactos/compartido" className="nav__item" onClick={onNavigate}>
                  <Icon name="contacts" />
                  Directorio compartido
                </Link>
              </li>
            </>
          )}
          <li><Link to="/caja" className="nav__item" onClick={onNavigate}><Icon name="wallet" />Caja</Link></li>
          {upcoming.map((item) => (
            <li key={item.label}>
              <span className="nav__item nav__item--disabled">
                <Icon name={item.icon} />
                <span>{item.label}</span>
                <span className="badge">Próximamente</span>
              </span>
            </li>
          ))}
        </ul>
      </nav>

      <div className="sidebar__bottom">
        <nav aria-label="Cuenta" className="nav">
          <ul>
            <li>
              <Link to="/seguridad" className="nav__item" onClick={onNavigate}>
                <Icon name="shield" />
                Seguridad
              </Link>
            </li>
          </ul>
        </nav>
        <div className="user-card">
          <p className="user-card__email">{me.email}</p>
          <p className="user-card__role">{roleLabels(me)}</p>
          <ThemeSwitch />
          <SignOutButton className="button button--ghost button--block">
            <Icon name="logout" />
            Cerrar sesión
          </SignOutButton>
        </div>
      </div>
    </div>
  );
}

/**
 * Phone navigation drawer (Modern Web Guidance `navigation-drawer`): a modal <dialog>
 * whose sheet sits in a horizontal scroll-snap container, so a swipe closes it
 * natively. An IntersectionObserver closes the dialog once the sheet is off-screen.
 */
function NavDrawer({ open, onClosed }: { open: boolean; onClosed: () => void }) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const sheetRef = useRef<HTMLElement>(null);
  const canAnimate = typeof IntersectionObserver === "function";

  function closeNow() {
    const dialog = dialogRef.current;
    if (dialog?.open && typeof dialog.close === "function") dialog.close();
    else onClosed();
  }

  function close() {
    const scroller = scrollerRef.current;
    // Slide out; the observer closes the dialog when the sheet has left the screen.
    if (canAnimate && scroller) scroller.scrollTo({ left: scroller.offsetWidth, behavior: "auto" });
    else closeNow();
  }

  useEffect(() => {
    const dialog = dialogRef.current;
    const sheet = sheetRef.current;
    if (!canAnimate || !dialog || !sheet) return;
    const visible = 1 / window.innerWidth;
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries.at(-1);
        if (entry && entry.intersectionRatio < visible && dialog.open) dialog.close();
      },
      { root: dialog, threshold: [visible, 1] },
    );
    observer.observe(sheet);
    return () => observer.disconnect();
  }, [canAnimate]);

  useEffect(() => {
    const dialog = dialogRef.current;
    const scroller = scrollerRef.current;
    if (!open || !dialog || !scroller || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    let cancelled = false;
    void (async () => {
      if (!CSS.supports?.("scroll-initial-target", "nearest")) {
        // Start at the closed stop so the scroll below animates the sheet in.
        scroller.scrollTo({ left: scroller.offsetWidth, behavior: "instant" });
        await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      }
      if (!cancelled) scroller.scrollTo({ left: 0, behavior: "auto" });
    })();
    return () => {
      cancelled = true;
    };
  }, [open]);

  return (
    <dialog
      ref={dialogRef}
      id="menu-drawer"
      className="drawer"
      aria-label="Menú"
      onClose={onClosed}
      onCancel={(event) => {
        if (event.cancelable) {
          event.preventDefault();
          close();
        }
      }}
      onClick={(event) => {
        if (!sheetRef.current?.contains(event.target as Node)) close();
      }}
    >
      <div ref={scrollerRef} className="drawer__scroller">
        <section ref={sheetRef} className="drawer__sheet">
          {open && (
            <>
              <button type="button" className="icon-button drawer__close" aria-label="Cerrar menú" autoFocus onClick={close}>
                <Icon name="close" />
              </button>
              <SidebarContent onNavigate={closeNow} />
            </>
          )}
        </section>
      </div>
    </dialog>
  );
}

export function AppShell({ page, children }: { page: string; children: ReactNode }) {
  const me = useMe();
  const [drawerOpen, setDrawerOpen] = useState(false);
  const inSchool = me.role !== null;
  // The "Centro de estudiantes" menu item depends on whether this user is on the Electoral commission.
  useEffect(() => {
    if (inSchool) void loadCommissionStatus();
    return resetCommissionStatus;
  }, [inSchool, me.tenantId]);
  return (
    <div className="shell">
      <a className="skip-link" href="#contenido">
        Saltar al contenido
      </a>
      <aside className="sidebar" aria-label="Barra lateral">
        <SidebarContent />
      </aside>
      <div className="shell__body">
        <header className="topbar">
          <button
            type="button"
            className="icon-button topbar__menu"
            aria-label="Abrir menú"
            aria-expanded={drawerOpen}
            aria-controls="menu-drawer"
            onClick={() => setDrawerOpen(true)}
          >
            <Icon name="menu" />
          </button>
          <span className="topbar__brand">
            <BrandMark />
            <span className="topbar__names">
              Centro de Estudiantes
              {me.tenantName !== null && (
                <span className="topbar__school">
                  <bdi>{me.tenantName}</bdi>
                </span>
              )}
            </span>
          </span>
          <span className="chip">
            <span className="chip__muted">Centro /</span> {page}
          </span>
        </header>
        <main id="contenido" className="content" tabIndex={-1}>
          {children}
        </main>
      </div>
      <NavDrawer open={drawerOpen} onClosed={() => setDrawerOpen(false)} />
      {/* Outside the drawer: a leave question asked from the phone menu outlives it. */}
      <LeaveConfirmHost />
    </div>
  );
}
