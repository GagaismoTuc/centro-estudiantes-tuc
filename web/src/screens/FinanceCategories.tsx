import { useEffect, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { FormStatus, type Status } from "../components/Form.tsx";

type Category = components["schemas"]["FinanceCategory"];
type Props = { categories: Category[]; used: Set<string> | null; officer: boolean; reload: () => Promise<void>; authError: (code: string) => void };

export function FinanceCategories({ categories, used, officer, reload, authError }: Props) {
  const [name, setName] = useState("");
  const [direction, setDirection] = useState<"income" | "expense">("income");
  const [editing, setEditing] = useState<Category | null>(null);
  const [editName, setEditName] = useState("");
  const [confirm, setConfirm] = useState<{ category: Category; operation: "archive" | "delete" } | null>(null);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<Status>(null);
  const [error, setError] = useState("");
  const nameRef = useRef<HTMLInputElement>(null);
  const editRef = useRef<HTMLInputElement>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const pendingFocus = useRef<"name" | "edit" | "status" | null>(null);
  useEffect(() => {
    if (pendingFocus.current === "name") nameRef.current?.focus();
    if (pendingFocus.current === "edit") editRef.current?.focus();
    if (pendingFocus.current === "status") statusRef.current?.focus();
    pendingFocus.current = null;
  });
  async function save(operation: "add" | "rename" | "archive" | "delete", category?: Category) {
    if (busy) return;
    const value = operation === "add" ? name.trim() : editName.trim();
    if ((operation === "add" || operation === "rename") && !value) {
      if (operation === "add") setEditing(null);
      setError("Escrib\u00ed un nombre."); pendingFocus.current = operation === "add" ? "name" : "edit"; return;
    }
    setBusy(true); setError(""); setStatus(null);
    const result = operation === "add" ? await call(apiClient.POST("/finance/categories", { body: { name: value, direction } }))
      : operation === "rename" ? await call(apiClient.PATCH("/finance/categories/{id}", { params: { path: { id: category!.id } }, body: { name: value } }))
      : operation === "archive" ? await call(apiClient.POST("/finance/categories/{id}/archive", { params: { path: { id: category!.id } } }))
      : await call(apiClient.DELETE("/finance/categories/{id}", { params: { path: { id: category!.id } } }));
    if (result.ok) {
      setName(""); setEditing(null); setConfirm(null); setStatus({ tone: "ok", text: "Se actualiz\u00f3 la categor\u00eda." }); await reload();
    } else {
      authError(result.code);
      if ((operation === "add" || operation === "rename") && (result.code === "name_required" || result.code === "category_exists")) {
        setError(errorMessage(result.code)); pendingFocus.current = operation === "add" ? "name" : "edit";
      } else { setConfirm(null); setStatus({ tone: "error", text: errorMessage(result.code) }); pendingFocus.current = "status"; }
    }
    setBusy(false);
  }
  return <section className="panel">
    <h2>{"Categor\u00edas"}</h2><FormStatus ref={statusRef} status={status} />
    {officer && <form noValidate onSubmit={(e) => { e.preventDefault(); void save("add"); }}>
      <div className="field"><label htmlFor="category-new-name">{"Nombre de la categor\u00eda"}</label><input id="category-new-name" ref={nameRef} value={name} aria-invalid={error && !editing ? true : undefined} aria-describedby={error && !editing ? "category-error" : undefined} onChange={(e) => { setName(e.target.value); setError(""); }} />{error && !editing && <p id="category-error" role="alert">{error}</p>}</div>
      <div className="field"><label htmlFor="category-direction">Sentido</label><select id="category-direction" value={direction} onChange={(e) => setDirection(e.target.value as "income" | "expense")}><option value="income">Ingresos</option><option value="expense">Gastos</option></select></div>
      <button className="button button--primary" disabled={busy}>{"Agregar categor\u00eda"}</button>
    </form>}
    {(["income", "expense"] as const).map((dir) => <div key={dir}><h3>{dir === "income" ? "Ingresos" : "Gastos"}</h3><ul>{categories.filter((c) => c.direction === dir).map((c) => <li key={c.id}>
      <strong>{c.name}</strong>{!c.active && " (Archivada)"}{c.reserved && <span> {"Reservada para los pagos de cuotas"}</span>}
      {officer && !c.reserved && <>
        {editing?.id === c.id ? <form noValidate onSubmit={(e) => { e.preventDefault(); void save("rename", c); }}><label htmlFor="category-edit">Nuevo nombre</label><input id="category-edit" ref={editRef} value={editName} aria-invalid={error ? true : undefined} aria-describedby={error ? "category-edit-error" : undefined} onChange={(e) => { setEditName(e.target.value); setError(""); }} />{error && <p id="category-edit-error" role="alert">{error}</p>}<button disabled={busy}>Guardar nombre</button><button type="button" onClick={() => { setEditing(null); setError(""); }}>Cancelar</button></form>
          : <button type="button" disabled={busy} onClick={() => { setEditing(c); setEditName(c.name); setError(""); pendingFocus.current = "edit"; }}>Renombrar</button>}
        {used !== null && (used.has(c.id) ? (c.active && <button type="button" disabled={busy} onClick={() => setConfirm({ category: c, operation: "archive" })}>Archivar</button>) : <button type="button" disabled={busy} onClick={() => setConfirm({ category: c, operation: "delete" })}>Borrar</button>)}
      </>}
    </li>)}</ul></div>)}
    {confirm && <ConfirmDialog title={confirm.operation === "archive" ? "Archivar categor\u00eda" : "Borrar categor\u00eda"} confirmLabel={confirm.operation === "archive" ? "Confirmar archivo" : "Confirmar borrado"} onCancel={() => setConfirm(null)} onConfirm={() => { void save(confirm.operation, confirm.category); }}><p>{confirm.category.name}</p></ConfirmDialog>}
  </section>;
}
