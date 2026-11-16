import { useEffect, useRef, useState } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { ConfirmDialog } from "../components/ConfirmDialog.tsx";
import { Field, FormStatus, useFormErrors, type Status } from "../components/Form.tsx";

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
  const createForm = useFormErrors();
  const editForm = useFormErrors();
  const editRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (editing) editRef.current?.focus();
  }, [editing]);
  async function save(operation: "add" | "rename" | "archive" | "delete", category?: Category) {
    if (busy) return;
    const value = operation === "add" ? name.trim() : editName.trim();
    setBusy(true); setStatus(null);
    const result = operation === "add" ? await call(apiClient.POST("/finance/categories", { body: { name: value, direction } }))
      : operation === "rename" ? await call(apiClient.PATCH("/finance/categories/{id}", { params: { path: { id: category!.id } }, body: { name: value } }))
      : operation === "archive" ? await call(apiClient.POST("/finance/categories/{id}/archive", { params: { path: { id: category!.id } } }))
      : await call(apiClient.DELETE("/finance/categories/{id}", { params: { path: { id: category!.id } } }));
    if (result.ok) {
      setName(""); setEditing(null); setConfirm(null); setStatus({ tone: "ok", text: "Se actualiz\u00f3 la categor\u00eda." }); await reload();
    } else {
      authError(result.code);
      setStatus({ tone: "error", text: errorMessage(result.code) });
      if (operation === "rename") editForm.failed(result.code);
      else { setEditing(null); setConfirm(null); createForm.failed(result.code); }
    }
    setBusy(false);
  }
  return <section className="panel">
    <h2>{"Categor\u00edas"}</h2><FormStatus status={status} {...(editing ? editForm.statusProps : createForm.statusProps)} />
    {officer && <form {...createForm.formProps} onSubmit={(e) => { e.preventDefault(); setEditing(null); if (createForm.validate({ name: (v) => v.trim() ? null : "Escrib\u00ed un nombre." })) void save("add"); }}>
      <Field label={"Nombre de la categor\u00eda"} {...createForm.field("name")} value={name} onChange={(e) => setName(e.target.value)} />
      <div className="field"><label htmlFor="category-direction">Sentido</label><select id="category-direction" value={direction} onChange={(e) => setDirection(e.target.value as "income" | "expense")}><option value="income">Ingresos</option><option value="expense">Gastos</option></select></div>
      <button className="button button--primary" disabled={busy}>{"Agregar categor\u00eda"}</button>
    </form>}
    {(["income", "expense"] as const).map((dir) => <div key={dir}><h3>{dir === "income" ? "Ingresos" : "Gastos"}</h3><ul>{categories.filter((c) => c.direction === dir).map((c) => <li key={c.id}>
      <strong>{c.name}</strong>{!c.active && " (Archivada)"}{c.reserved && <span> {"Reservada para los pagos de cuotas"}</span>}
      {officer && !c.reserved && <>
        {editing?.id === c.id ? <form {...editForm.formProps} onSubmit={(e) => { e.preventDefault(); if (editForm.validate({ name: (v) => v.trim() ? null : "Escrib\u00ed un nombre." })) void save("rename", c); }}><Field label="Nuevo nombre" ref={editRef} {...editForm.field("name")} value={editName} onChange={(e) => setEditName(e.target.value)} /><button disabled={busy}>Guardar nombre</button><button type="button" onClick={() => { setEditing(null); editForm.clear(); }}>Cancelar</button></form>
          : <button type="button" disabled={busy} onClick={() => { setEditing(c); setEditName(c.name); editForm.clear(); }}>Renombrar</button>}
        {used !== null && (used.has(c.id) ? (c.active && <button type="button" disabled={busy} onClick={() => setConfirm({ category: c, operation: "archive" })}>Archivar</button>) : <button type="button" disabled={busy} onClick={() => setConfirm({ category: c, operation: "delete" })}>Borrar</button>)}
      </>}
    </li>)}</ul></div>)}
    {confirm && <ConfirmDialog title={confirm.operation === "archive" ? "Archivar categor\u00eda" : "Borrar categor\u00eda"} confirmLabel={confirm.operation === "archive" ? "Confirmar archivo" : "Confirmar borrado"} onCancel={() => setConfirm(null)} onConfirm={() => { void save(confirm.operation, confirm.category); }}><p>{confirm.category.name}</p></ConfirmDialog>}
  </section>;
}
