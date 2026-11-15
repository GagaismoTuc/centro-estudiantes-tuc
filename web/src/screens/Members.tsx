import { useCallback, useEffect, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { useSession } from "../auth/session.tsx";
import { FormStatus, type Status } from "../components/Form.tsx";
import { PageTitle, useDocumentTitle } from "../components/Page.tsx";

type Member = components["schemas"]["SchoolMember"];
type RosterMember = components["schemas"]["RosterMember"];
const labels: Record<components["schemas"]["Role"], string> = {
  school_admin: "Administrador de la escuela",
  student: "Alumno",
  officer: "Integrante de la comisi\u00f3n directiva",
};
const positions = ["Presidente", "Vicepresidente", "Secretario", "Tesorero"];

export function Members() {
  useDocumentTitle("Miembros");
  const { refresh } = useSession();
  const [members, setMembers] = useState<Member[]>([]);
  const [roster, setRoster] = useState<RosterMember[]>([]);
  const [member, setMember] = useState("");
  const [position, setPosition] = useState("Presidente");
  const [status, setStatus] = useState<Status>(null);
  const [busy, setBusy] = useState(false);
  const statusRef = useRef<HTMLDivElement>(null);
  useEffect(() => { if (status?.tone === "error") statusRef.current?.focus(); }, [status]);
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

  async function assign() {
    if (!member) return;
    setBusy(true); setStatus(null);
    const result = await call(apiClient.POST("/members/{memberId}/officer", { params: { path: { memberId: member } }, body: { position } }));
    if (result.ok) { setStatus({ tone: "ok", text: "Se asign\u00f3 el cargo." }); await load(); }
    else failed(result.code);
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
    <section className="panel"><h2>Asignar cargo</h2>
      <form noValidate onSubmit={(e) => { e.preventDefault(); void assign(); }}>
        <div className="field"><label htmlFor="member">{"Alumno del padr\u00f3n"}</label>
          <select id="member" value={member} onChange={(e) => setMember(e.target.value)}>
            <option value="">{"Eleg\u00ed un alumno"}</option>
            {roster.map((m) => <option key={m.id} value={m.id}>{m.fullName}</option>)}
          </select></div>
        <div className="field"><label htmlFor="position">Cargo</label>
          <select id="position" value={position} onChange={(e) => setPosition(e.target.value)}>{positions.map((p) => <option key={p}>{p}</option>)}</select></div>
        <button className="button button--primary" disabled={busy || !member}>Asignar cargo</button>
      </form>
    </section>
    <section className="panel"><h2>Miembros</h2>
      <ul>{members.map((m, i) => <li key={m.email ?? m.rosterMemberId ?? i}>
        <bdi>{m.fullName ?? m.email}</bdi>{m.fullName && m.email && <> {"\u2014"} <bdi>{m.email}</bdi></>}{m.email === null && <> {"\u2014"} <span>sin cuenta</span></>}
        <ul>{m.roles.map((role) => <li key={role}>{labels[role]}{role === "officer" && m.position ? ` \u2014 ${m.position}` : ""}</li>)}</ul>
        {m.position && m.rosterMemberId && <button type="button" className="button button--secondary" disabled={busy} aria-label={`Quitar cargo de ${m.fullName ?? m.email ?? ""}`} onClick={() => void remove(m.rosterMemberId!)}>Quitar cargo</button>}
      </li>)}</ul>
    </section>
  </>;
}
