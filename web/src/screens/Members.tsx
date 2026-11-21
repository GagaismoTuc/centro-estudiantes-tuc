import { useCallback, useEffect, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { roleLabel, sortRoles, useSession } from "../auth/session.tsx";
import { checks, Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";

type Member = components["schemas"]["SchoolMember"];
type RosterMember = components["schemas"]["RosterMember"];
const positions = ["Presidente", "Vicepresidente", "Secretario", "Tesorero"];

export function Members() {
  useDocumentTitle("Miembros");
  const { refresh } = useSession();
  const invitationForm = useFormErrors();
  const [invitationStatus, setInvitationStatus] = useState<Status>(null);
  const [members, setMembers] = useState<Member[]>([]);
  const [roster, setRoster] = useState<RosterMember[]>([]);
  const [member, setMember] = useState("");
  const [position, setPosition] = useState("Presidente");
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const [fieldError, setFieldError] = useState<{ field: "member" | "position"; text: string } | null>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const memberRef = useRef<HTMLSelectElement>(null);
  const positionRef = useRef<HTMLSelectElement>(null);
  useEffect(() => { if (status?.tone === "error") statusRef.current?.focus(); }, [status]);
  useEffect(() => {
    if (fieldError) (fieldError.field === "position" ? positionRef : memberRef).current?.focus();
  }, [fieldError]);
  const failed = useCallback((code: string) => {
    if (code === "mfa_required" || code === "unauthenticated") void refresh();
    setStatus({ tone: "error", text: errorMessage(code) });
  }, [refresh]);
  const load = useCallback(async () => {
    const [m, r] = await Promise.all([call(apiClient.GET("/members")), call(apiClient.GET("/roster"))]);
    if (m.ok) setMembers(m.data); else failed(m.code);
    if (r.ok) setRoster(r.data); else failed(r.code);
  }, [failed]);
  useEffect(() => { void load(); }, [load]);

  async function invite(role: "veedor" | "school_admin") {
    setInvitationStatus(null);
    if (!invitationForm.validate({ email: (v) => (v.trim() ? checks.email(v) : "Escribí el mail de la persona.") })) return;
    setBusy(true);
    const result = await call(apiClient.POST("/members/invitations", { body: { email: invitationForm.value("email").trim(), role } }));
    if (result.ok) setInvitationStatus({ tone: "ok", text: "Se gener\u00f3 la invitaci\u00f3n. Si no llega, pod\u00e9s reenviarla." });
    else {
      if (result.code === "mfa_required" || result.code === "unauthenticated") void refresh();
      setInvitationStatus({ tone: "error", text: errorMessage(result.code) });
      invitationForm.failed(result.code);
    }
    setBusy(false);
  }
  async function assign() {
    if (!member) return;
    setBusy(true); setStatus(null); setFieldError(null);
    const result = await call(apiClient.POST("/members/{memberId}/officer", { params: { path: { memberId: member } }, body: { position } }));
    if (result.ok) { setStatus({ tone: "ok", text: "Se asign\u00f3 el cargo." }); await load(); }
    else if (result.code === "position_taken" || result.code === "invalid_position" || result.code === "already_officer") {
      setFieldError({ field: result.code === "already_officer" ? "member" : "position", text: errorMessage(result.code) });
    } else failed(result.code);
    setBusy(false);
  }
  async function remove(id: string) {
    setBusy(true); setStatus(null);
    const result = await call(apiClient.DELETE("/members/{memberId}/officer", { params: { path: { memberId: id } } }));
    if (result.ok) { setStatus({ tone: "ok", text: "Se quit\u00f3 el cargo." }); await load(); }
    else failed(result.code);
    setBusy(false);
  }
  return <>
    <header className="page-header"><PageTitle eyebrow="Escuela">Miembros</PageTitle></header>
    <FormStatus status={status} ref={statusRef} />
    <section className="panel"><h2>Invitar autoridades</h2>
      <form {...invitationForm.formProps} onSubmit={(e) => { e.preventDefault(); void invite("veedor"); }}>
        <FormStatus status={invitationStatus} {...invitationForm.statusProps} />
        <Field {...invitationForm.field("email")} label={"Correo electr\u00f3nico"} type="email" autoComplete="email" maxLength={254} />
        <button className="button button--primary" disabled={busy}>Invitar Veedor</button>{" "}
        <button type="button" className="button button--secondary" disabled={busy} onClick={() => void invite("school_admin")}>Invitar administrador</button>
      </form>
    </section>
    <section className="panel"><h2>Asignar cargo</h2>
      <form noValidate onSubmit={(e) => { e.preventDefault(); void assign(); }}>
        <div className="field"><label htmlFor="member">{"Alumno del padr\u00f3n"}</label>
          <select id="member" ref={memberRef} value={member} aria-invalid={fieldError?.field === "member" || undefined} aria-describedby={fieldError?.field === "member" ? "member-error" : undefined} onChange={(e) => { setMember(e.target.value); setFieldError(null); }}>
            <option value="">{"Eleg\u00ed un alumno"}</option>
            {roster.map((m) => <option key={m.id} value={m.id}>{m.fullName}</option>)}
          </select>{fieldError?.field === "member" && <p id="member-error" role="alert">{fieldError.text}</p>}</div>
        <div className="field"><label htmlFor="position">Cargo</label>
          <select id="position" ref={positionRef} value={position} aria-invalid={fieldError?.field === "position" || undefined} aria-describedby={fieldError?.field === "position" ? "position-error" : undefined} onChange={(e) => { setPosition(e.target.value); setFieldError(null); }}>{positions.map((p) => <option key={p}>{p}</option>)}</select>{fieldError?.field === "position" && <p id="position-error" role="alert">{fieldError.text}</p>}</div>
        <button className="button button--primary" disabled={busy || !member}>Asignar cargo</button>
      </form>
    </section>
    <section className="panel"><h2>Miembros</h2>
      <ul>{members.map((m, i) => <li key={m.email ?? m.rosterMemberId ?? i}>
        <bdi>{m.fullName ?? m.email}</bdi>{m.fullName && m.email && <> {"\u2014"} <bdi>{m.email}</bdi></>}{m.email === null && <> {"\u2014"} <span>sin cuenta</span></>}
        <ul>{sortRoles(m.roles).map((role) => <li key={role}>{roleLabel[role]}{role === "officer" && m.position ? ` \u2014 ${m.position}` : ""}</li>)}</ul>
        {m.position && m.rosterMemberId && <button type="button" className="button button--secondary" disabled={busy} aria-label={`Quitar cargo de ${m.fullName ?? m.email ?? ""}`} onClick={() => void remove(m.rosterMemberId!)}>Quitar cargo</button>}
      </li>)}</ul>
    </section>
  </>;
}
