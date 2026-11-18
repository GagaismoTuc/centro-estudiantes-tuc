import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());
const categories = [
  { id: "c1", name: "Ventas", direction: "income", active: true, reserved: false },
  { id: "c2", name: "Cuotas", direction: "income", active: true, reserved: true },
  { id: "c3", name: "Viajes", direction: "expense", active: true, reserved: false },
];
const entry = { id: "e1", categoryId: "c1", direction: "income", date: "2026-11-10", amountCents: 200000, description: "Venta de tortas", recordedBy: "Ana Ficticia", recordedAt: "2026-11-14T12:00:00Z", hasReceipt: false };
const reads = { "GET /finance/categories": ok(categories), "GET /finance/balance": ok({ balanceCents: 200000 }), "GET /finance/entries": ok([entry]) };

test("R15 students read the ledger and balance without an entry form", async () => {
  mockApi({ "GET /auth/me": ok(me()), ...reads });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("$ 2.000,00")).toBeTruthy();
  expect(screen.getByText(/Registrado por Ana Ficticia/)).toBeTruthy();
  expect(screen.getByText(/Ventas.*2026-11-10/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Nuevo movimiento" })).toBeNull();
});

test("R15 school admins read the ledger without an entry form", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })), ...reads });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("$ 2.000,00")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Nuevo movimiento" })).toBeNull();
});

test("R15 officers record movements and R13 blocks double submission", async () => {
  const sent = mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads, "POST /finance/entries": ok(entry, 201) });
  const handler = vi.mocked(fetch).getMockImplementation()!;
  let release!: () => void;
  const pending = new Promise<void>((resolve) => { release = resolve; });
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    if ((input as Request).method === "POST") await pending;
    return handler(input, init);
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-10" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Venta de tortas" } });
  const save = screen.getByRole("button", { name: "Guardar" }); fireEvent.click(save);
  expect((save as HTMLButtonElement).disabled).toBe(true); fireEvent.click(save); release();
  await waitFor(() => expect(sentTo(sent, "POST /finance/entries")).toHaveLength(1));
  expect(sentTo(sent, "POST /finance/entries")[0]?.body).toMatchObject({ categoryId: "c1", direction: "income", date: "2026-11-10", amountCents: 200000, description: "Venta de tortas" });
});

test("R15 mfa_required returns officers to second factor", async () => {
  mockApi({ "GET /auth/me": [ok(me({ role: "officer", mfa: "satisfied" })), ok(me({ role: "officer", mfa: "pending" }))], ...reads, "POST /finance/entries": fail(403, "mfa_required") });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-10" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Venta" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  await waitFor(() => expect(window.location.pathname).toBe("/seguridad/verificar"));
});

test("R2 and R7 the screen preserves full int64 cents without rounding", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads });
  const handler = vi.mocked(fetch).getMockImplementation()!;
  let sent = "";
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const req = input as Request;
    const path = new URL(req.url).pathname;
    if (req.method === "GET" && path === "/api/finance/balance") return new Response('{"balanceCents":9223372036854775807}', { status: 200, headers: { "Content-Type": "application/json" } });
    if (req.method === "POST" && path === "/api/finance/entries") { sent = await req.text(); return new Response(JSON.stringify(entry), { status: 201, headers: { "Content-Type": "application/json" } }); }
    return handler(input, init);
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("$ 92.233.720.368.547.758,07")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-10" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "92233720368547758,07" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Fixture" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  await waitFor(() => expect(sent).toContain('"amountCents":9223372036854775807'));
});

test("R15 invalid form fields are announced and focused", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  const category = screen.getByLabelText("Categor\u00eda");
  expect(category.getAttribute("aria-invalid")).toBe("true");
  expect(document.activeElement).toBe(category);
  fireEvent.change(category, { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "0" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  const amount = screen.getByLabelText("Monto en pesos");
  await waitFor(() => expect(amount.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(amount);
  expect(document.getElementById(amount.getAttribute("aria-describedby") ?? "")?.textContent).toContain("monto positivo");
});

test("R8 officers manage categories by direction and see only permitted actions", async () => {
  const sent = mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads,
    "POST /finance/categories": ok({ id: "c4", name: "Buzos", direction: "expense", active: true, reserved: false }, 201),
    "PATCH /finance/categories/c1": ok({ ...categories[0], name: "Kiosco" }),
    "POST /finance/categories/c1/archive": ok({ ...categories[0], active: false }),
    "DELETE /finance/categories/c3": new Response(null, { status: 204 }),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Categor\u00edas" }));
  expect(await screen.findByText("Reservada para los pagos de cuotas")).toBeTruthy();
  const reserved = screen.getByText("Reservada para los pagos de cuotas").closest("li")!;
  expect(reserved.querySelector("button")).toBeNull();
  const used = screen.getByText("Ventas", { selector: "strong" }).closest("li")!;
  const unused = screen.getByText("Viajes", { selector: "strong" }).closest("li")!;
  expect(within(used).getByRole("button", { name: "Archivar" })).toBeTruthy();
  expect(within(used).queryByRole("button", { name: "Borrar" })).toBeNull();
  expect(within(unused).getByRole("button", { name: "Borrar" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Agregar categor\u00eda" }));
  const name = screen.getByLabelText("Nombre de la categor\u00eda");
  await waitFor(() => expect(name.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(name);
  fireEvent.change(screen.getByLabelText("Nombre de la categor\u00eda"), { target: { value: "Buzos" } });
  fireEvent.change(screen.getByLabelText("Sentido"), { target: { value: "expense" } });
  fireEvent.click(screen.getByRole("button", { name: "Agregar categor\u00eda" }));
  await waitFor(() => expect(sentTo(sent, "POST /finance/categories")).toHaveLength(1));
  fireEvent.click(within(used).getByRole("button", { name: "Renombrar" }));
  fireEvent.change(screen.getByLabelText("Nuevo nombre"), { target: { value: "Kiosco" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar nombre" }));
  await waitFor(() => expect(sentTo(sent, "PATCH /finance/categories/c1")).toHaveLength(1));
  fireEvent.click(within(used).getByRole("button", { name: "Archivar" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirmar archivo" }));
  await waitFor(() => expect(sentTo(sent, "POST /finance/categories/c1/archive")).toHaveLength(1));
  fireEvent.click(within(unused).getByRole("button", { name: "Borrar" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirmar borrado" }));
  await waitFor(() => expect(sentTo(sent, "DELETE /finance/categories/c3")).toHaveLength(1));
});

test("R1 created expense appears in the movement form", async () => {
  const added = { id: "c4", name: "Buzos", direction: "expense", active: true, reserved: false };
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads,
    "GET /finance/categories": [ok(categories), ok([...categories, added])],
    "POST /finance/categories": ok(added, 201),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Categor\u00edas" }));
  await screen.findByText("Reservada para los pagos de cuotas");
  fireEvent.change(screen.getByLabelText("Nombre de la categor\u00eda"), { target: { value: "Buzos" } });
  fireEvent.change(screen.getByLabelText("Sentido"), { target: { value: "expense" } });
  fireEvent.click(screen.getByRole("button", { name: "Agregar categor\u00eda" }));
  await screen.findByText("Buzos", { selector: "strong" });
  fireEvent.click(screen.getByRole("button", { name: "Nuevo movimiento" }));
  fireEvent.change(screen.getByLabelText("Tipo"), { target: { value: "expense" } });
  expect(await screen.findByRole("option", { name: "Buzos" })).toBeTruthy();
});

test("R2 renaming changes the category shown on historical movements", async () => {
  const renamed = [{ ...categories[0]!, name: "Kiosco" }, ...categories.slice(1)];
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads,
    "GET /finance/categories": [ok(categories), ok(renamed)],
    "PATCH /finance/categories/c1": ok(renamed[0]),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Categor\u00edas" }));
  const used = (await screen.findByText("Ventas", { selector: "strong" })).closest("li")!;
  fireEvent.click(within(used).getByRole("button", { name: "Renombrar" }));
  fireEvent.change(screen.getByLabelText("Nuevo nombre"), { target: { value: "Kiosco" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar nombre" }));
  expect(await screen.findByText(/Kiosco.*2026-11-10/)).toBeTruthy();
});

test("R3 archived categories disappear from entry choices but remain on historical movements", async () => {
  const archived = [{ ...categories[0]!, active: false }, ...categories.slice(1)];
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads,
    "GET /finance/categories": [ok(categories), ok(archived)],
    "POST /finance/categories/c1/archive": ok(archived[0]),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Categor\u00edas" }));
  const used = (await screen.findByText("Ventas", { selector: "strong" })).closest("li")!;
  fireEvent.click(within(used).getByRole("button", { name: "Archivar" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirmar archivo" }));
  await screen.findByText("(Archivada)", { exact: false });
  expect(screen.getByText(/Ventas.*2026-11-10/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Nuevo movimiento" }));
  expect(screen.queryByRole("option", { name: "Ventas" })).toBeNull();
});

const reversedOriginal = { ...entry, reversalId: "e2", reversedBy: "Beto Ficticio", reversedOn: "2026-11-14" };
const reversal = { ...entry, id: "e2", direction: "expense", date: "2026-11-14", description: "Reversión: Venta de tortas", recordedBy: "Beto Ficticio", reversesId: "e1" };

test("R9 officers revert a movement only after confirming, and cancelling sends nothing", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads,
    "GET /finance/entries": [ok([entry]), ok([reversedOriginal, reversal])],
    "GET /finance/balance": [ok({ balanceCents: 200000 }), ok({ balanceCents: 0 })],
    "POST /finance/entries/e1/reversal": ok(reversal, 201),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: /^Revertir/ }));
  let dialog = screen.getByRole("dialog", { name: "Revertir movimiento" });
  expect(dialog.textContent).toContain("¿Revertís este movimiento? Se va a cargar un movimiento opuesto con fecha de hoy.");
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(sentTo(sent, "POST /finance/entries/e1/reversal")).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: /^Revertir/ }));
  dialog = screen.getByRole("dialog", { name: "Revertir movimiento" });
  fireEvent.click(within(dialog).getByRole("button", { name: "Revertir" }));
  expect(await screen.findByText("$ 0,00")).toBeTruthy();
  expect(sentTo(sent, "POST /finance/entries/e1/reversal")).toHaveLength(1);
  expect(screen.getByText("Revertido por Beto Ficticio el 14/11/2026")).toBeTruthy();
});

test("R9 Revertir is shown only to officers", async () => {
  mockApi({ "GET /auth/me": ok(me()), ...reads });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("Venta de tortas")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /^Revertir/ })).toBeNull();
});

test("R9 reversed movements and Reversals have no Revertir", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads, "GET /finance/entries": ok([reversedOriginal, reversal]) });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("Reversión: Venta de tortas")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Nuevo movimiento" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: /^Revertir/ })).toBeNull();
});

test("R1 a reversed original shows who reversed it and when", async () => {
  mockApi({ "GET /auth/me": ok(me()), ...reads, "GET /finance/entries": ok([reversedOriginal, reversal]) });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  expect(await screen.findByText("Revertido por Beto Ficticio el 14/11/2026")).toBeTruthy();
  expect(screen.getAllByText(/^Revertido por/)).toHaveLength(1);
});

test("R9 a failed reversal moves focus to the message", async () => {
  mockApi({ "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })), ...reads, "POST /finance/entries/e1/reversal": fail(409, "already_reversed") });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: /^Revertir/ }));
  fireEvent.click(within(screen.getByRole("dialog", { name: "Revertir movimiento" })).getByRole("button", { name: "Revertir" }));
  const message = await screen.findByText(/ya fue revertido/);
  await waitFor(() => expect(document.activeElement?.contains(message)).toBe(true));
});

test("R1 and R5 creating a movement with a receipt uploads after saving and handles receipt failure", async () => {
  const withReceipt = { ...entry, hasReceipt: true };
  const uploaded = { id: "r1", contentType: "image/jpeg", sizeBytes: 12, uploadedAt: "2026-11-14T12:05:00Z" };
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    ...reads,
    "GET /finance/entries": [ok([entry]), ok([withReceipt])],
    "POST /finance/entries": ok(entry, 201),
    "POST /finance/entries/e1/receipts": ok(uploaded, 201),
  });
  window.history.replaceState(null, "", "/caja");
  const firstView = render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-10" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Venta de tortas" } });
  const fileInput = screen.getByLabelText("Comprobante (opcional)") as HTMLInputElement;
  fireEvent.change(fileInput, { target: { files: [new File([new Uint8Array([0xff, 0xd8, 0xff, 0xe0])], "ticket.jpg", { type: "image/jpeg" })] } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));

  await waitFor(() => expect(sentTo(sent, "POST /finance/entries/e1/receipts")).toHaveLength(1));
  expect(sentTo(sent, "POST /finance/entries")).toHaveLength(1);
  expect(await screen.findByText("Con comprobante")).toBeTruthy();
  firstView.unmount();

  // R5: entry succeeds (201) and receipt upload fails -> entry stays saved and exact R5 message appears.
  const sentR5 = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    ...reads,
    "GET /finance/entries": [ok([entry]), ok([entry])],
    "POST /finance/entries": ok(entry, 201),
    "POST /finance/entries/e1/receipts": fail(415, "unsupported_media_type"),
  });
  window.history.replaceState(null, "", "/caja"); render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nuevo movimiento" }));
  await screen.findByRole("option", { name: "Ventas" });
  fireEvent.change(screen.getByLabelText("Categor\u00eda"), { target: { value: "c1" } });
  fireEvent.change(screen.getByLabelText("Fecha"), { target: { value: "2026-11-10" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Descripci\u00f3n"), { target: { value: "Venta de tortas" } });
  fireEvent.change(screen.getByLabelText("Comprobante (opcional)"), {
    target: { files: [new File(["MZbad"], "factura.pdf", { type: "application/pdf" })] },
  });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));

  const r5Message = await screen.findByText("El movimiento se guard\u00f3, pero el comprobante no. Volv\u00e9 a subirlo desde el detalle.");
  expect(r5Message).toBeTruthy();
  expect(sentTo(sentR5, "POST /finance/entries")).toHaveLength(1);
  expect(sentTo(sentR5, "POST /finance/entries/e1/receipts")).toHaveLength(1);
  expect(screen.getByRole("button", { name: "Cerrar detalle" })).toBeTruthy();
});

test("R1, R6, and R7 detail downloads and uploads respect roles", async () => {
  const backed = { ...entry, hasReceipt: true };
  const r1 = { id: "r1", contentType: "image/jpeg", sizeBytes: 307200, uploadedAt: "2026-11-14T12:05:00Z" };
  const r2 = { id: "r2", contentType: "application/pdf", sizeBytes: 1024, uploadedAt: "2026-11-14T12:06:00Z" };

  // Student sees only "Con comprobante", no detail or download or upload.
  mockApi({
    "GET /auth/me": ok(me()),
    ...reads,
    "GET /finance/entries": ok([backed]),
  });
  window.history.replaceState(null, "", "/caja");
  const studentView = render(<App />);
  expect(await screen.findByText("Con comprobante")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Ver detalle/ })).toBeNull();
  expect(screen.queryByLabelText("Subir comprobante")).toBeNull();
  studentView.unmount();

  // School admin and Veedor see detail and download link, without upload control.
  for (const role of ["school_admin", "veedor"]) {
    mockApi({
      "GET /auth/me": ok(me({ role: role as never, roles: [role as never], mfa: "satisfied" })),
      ...reads,
      "GET /finance/entries": ok([backed]),
      "GET /finance/entries/e1/receipts": ok([r1]),
    });
    window.history.replaceState(null, "", "/caja");
    const view = render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /Ver detalle/ }));
    const link = await screen.findByRole("link", { name: "Descargar comprobante 1" });
    expect(link.getAttribute("href")).toBe("/api/finance/entries/e1/receipts/r1");
    expect(screen.queryByLabelText("Subir comprobante")).toBeNull();
    view.unmount();
  }

  // Officer opens detail, sees existing receipt, and uploads a second receipt.
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    ...reads,
    "GET /finance/entries": ok([backed]),
    "GET /finance/entries/e1/receipts": [ok([r1]), ok([r1, r2])],
    "POST /finance/entries/e1/receipts": ok(r2, 201),
  });
  window.history.replaceState(null, "", "/caja");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: /Ver detalle/ }));
  await screen.findByRole("link", { name: "Descargar comprobante 1" });
  fireEvent.change(screen.getByLabelText("Subir comprobante"), {
    target: { files: [new File(["%PDF-1.4\n"], "dorso.pdf", { type: "application/pdf" })] },
  });
  expect(await screen.findByRole("link", { name: "Descargar comprobante 2" })).toBeTruthy();
  expect(sentTo(sent, "POST /finance/entries/e1/receipts")).toHaveLength(1);
});

test("R2 and R3 detail upload shows Spanish error and moves focus to invalid input", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    ...reads,
    "GET /finance/entries/e1/receipts": ok([]),
    "POST /finance/entries/e1/receipts": fail(415, "unsupported_media_type"),
  });
  window.history.replaceState(null, "", "/caja");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: /Ver detalle/ }));
  const input = await screen.findByLabelText("Subir comprobante");
  fireEvent.change(input, {
    target: { files: [new File(["GIF89a"], "anim.gif", { type: "image/gif" })] },
  });
  const errMsg = await screen.findByText("El archivo tiene que ser JPG, PNG o PDF.");
  expect(input.getAttribute("aria-invalid")).toBe("true");
  expect(input.getAttribute("aria-describedby")).toContain(errMsg.id);
  await waitFor(() => {
    expect((input as HTMLInputElement).disabled).toBe(false);
    expect(document.activeElement).toBe(input);
  });

  // Empty file rejected cleanly.
  fireEvent.change(input, {
    target: { files: [new File([], "vacio.pdf", { type: "application/pdf" })] },
  });
  expect(await screen.findByText(/est\u00e1 vac\u00edo/)).toBeTruthy();
  expect(input.getAttribute("aria-invalid")).toBe("true");
  await waitFor(() => expect(document.activeElement).toBe(input));
});
