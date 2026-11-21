import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import type { Role } from "./auth/session.tsx";
import { fail, me, mockApi, noContent, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());

const dueSummary = {
  id: "d1",
  name: "Cuota de noviembre",
  amountCents: 200000,
  dueDate: "2026-11-30",
  createdAt: "2026-11-16T10:00:00Z",
  owingCount: 2,
  totalCount: 2,
};

const dueDetail = {
  ...dueSummary,
  members: [
    { rosterMemberId: "r1", fullName: "Ana Ficticia", status: "owing", paidCents: 0 },
    { rosterMemberId: "r2", fullName: "Beto Ficticio", status: "owing", paidCents: 0 },
  ],
};

const myDue = {
  id: "d1",
  name: "Cuota de noviembre",
  amountCents: 200000,
  dueDate: "2026-11-30",
  status: "owing",
  paidCents: 0,
};

test("R10 student sees Mis cuotas without full list or Nueva cuota", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me()),
    "GET /finance/dues/mine": ok([myDue]),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  expect(await screen.findByRole("heading", { name: "Mis cuotas" })).toBeTruthy();
  expect(await screen.findByText(/Debés \$ 2\.000,00 — vence el 30\/11\/2026/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Nueva cuota" })).toBeNull();
  expect(sentTo(sent, "GET /finance/dues")).toHaveLength(0);
});

test("R10 officer sees Nueva cuota, list, detail, Mis cuotas, and prevents double submit", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" })),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([myDue]),
    "GET /finance/dues/d1": ok(dueDetail),
    "POST /finance/dues": ok(dueDetail, 201),
  });
  const handler = vi.mocked(fetch).getMockImplementation()!;
  let release!: () => void;
  const pending = new Promise<void>((resolve) => { release = resolve; });
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    if ((input as Request).method === "POST") await pending;
    return handler(input, init);
  });

  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  expect(await screen.findByRole("button", { name: "Nueva cuota" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Mis cuotas" })).toBeTruthy();
  expect(await screen.findByText(/Debés \$ 2\.000,00 — vence el 30\/11\/2026/)).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Nueva cuota" }));
  fireEvent.change(screen.getByLabelText("Nombre"), { target: { value: "Cuota de noviembre" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Vencimiento"), { target: { value: "2026-11-30" } });
  const save = screen.getByRole("button", { name: "Guardar" });
  fireEvent.click(save);
  expect((save as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(save);
  release();

  await waitFor(() => expect(sentTo(sent, "POST /finance/dues")).toHaveLength(1));
  expect(sentTo(sent, "POST /finance/dues")[0]?.body).toEqual({
    name: "Cuota de noviembre",
    amountCents: 200000,
    dueDate: "2026-11-30",
  });

  fireEvent.click(await screen.findByRole("button", { name: "Ver detalle de Cuota de noviembre" }));
  expect(await screen.findByText("Ana Ficticia")).toBeTruthy();
  expect(screen.getByText("Beto Ficticio")).toBeTruthy();
});

test("R3 and R10 officer edits and deletes a due using ConfirmDialog", async () => {
  const updated = { ...dueDetail, name: "Cuota diciembre", amountCents: 250000, dueDate: "2026-12-15" };
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([myDue]),
    "PATCH /finance/dues/d1": ok(updated),
    "DELETE /finance/dues/d1": noContent(),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);

  fireEvent.click(await screen.findByRole("button", { name: "Editar Cuota de noviembre" }));
  fireEvent.change(screen.getByLabelText("Nombre"), { target: { value: "Cuota diciembre" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2500" } });
  fireEvent.change(screen.getByLabelText("Vencimiento"), { target: { value: "2026-12-15" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar cambios" }));

  await waitFor(() => expect(sentTo(sent, "PATCH /finance/dues/d1")).toHaveLength(1));
  expect(sentTo(sent, "PATCH /finance/dues/d1")[0]?.body).toEqual({
    name: "Cuota diciembre",
    amountCents: 250000,
    dueDate: "2026-12-15",
  });

  fireEvent.click(screen.getByRole("button", { name: "Borrar Cuota de noviembre" }));
  const dialog = await screen.findByRole("dialog");
  fireEvent.click(within(dialog).getByRole("button", { name: "Confirmar borrado" }));
  await waitFor(() => expect(sentTo(sent, "DELETE /finance/dues/d1")).toHaveLength(1));
});

test("R4 school admin and veedor read dues list and detail without edit controls", async () => {
  for (const role of ["school_admin", "veedor"]) {
    mockApi({
      "GET /auth/me": ok(me({ role: role as Role, mfa: "satisfied" })),
      "GET /finance/dues": ok([dueSummary]),
      "GET /finance/dues/d1": ok(dueDetail),
    });
    window.history.replaceState(null, "", "/cuotas");
    const { unmount } = render(<App />);
    expect(await screen.findByText("Cuota de noviembre")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Nueva cuota" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Editar/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Borrar/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Ver detalle de Cuota de noviembre" }));
    expect(await screen.findByText("Ana Ficticia")).toBeTruthy();
    unmount();
  }
});

test("CS-9 and CS-10 accessible validation focuses invalid field and server errors move focus", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /finance/dues": ok([]),
    "GET /finance/dues/mine": ok([]),
    "POST /finance/dues": fail(422, "empty_roster"),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nueva cuota" }));
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));

  const nameInput = screen.getByLabelText("Nombre") as HTMLInputElement;
  expect(nameInput.getAttribute("aria-invalid")).toBe("true");
  expect(document.getElementById(nameInput.getAttribute("aria-describedby") ?? "")?.textContent).toContain("Escribí un nombre.");
  expect(document.activeElement).toBe(nameInput);

  fireEvent.change(nameInput, { target: { value: "Cuota de noviembre" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Vencimiento"), { target: { value: "2026-11-30" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));

  const statusMsg = await screen.findByText(/padrón está vacío/i);
  await waitFor(() => expect(document.activeElement).toBe(statusMsg.closest(".form-status")));
});

test("R3 due_frozen error and R6 mfa_required refresh on Dues", async () => {
  mockApi({
    "GET /auth/me": [ok(me({ role: "officer", mfa: "satisfied" })), ok(me({ role: "officer", mfa: "pending" }))],
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([myDue]),
    "PATCH /finance/dues/d1": fail(409, "due_frozen"),
    "DELETE /finance/dues/d1": fail(403, "mfa_required"),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Editar Cuota de noviembre" }));
  fireEvent.click(screen.getByRole("button", { name: "Guardar cambios" }));
  expect(await screen.findByText(/ya tiene pagos o exenciones/i)).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Borrar Cuota de noviembre" }));
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Confirmar borrado" }));
  await waitFor(() => expect(screen.getByRole("heading", { name: /Verificá/i })).toBeTruthy());
});

test("R5 a due with payments shows Monto and Vencimiento disabled with the note, and only the name changes", async () => {
  const paidDue = { ...dueSummary, id: "d2", name: "Cuota de octubre", owingCount: 1, totalCount: 2 };
  const partial = { ...dueDetail, members: [dueDetail.members[0], { ...dueDetail.members[1], paidCents: 50000 }] };
  const sent = mockApi({
    "GET /auth/me": ok(officerMe()),
    "GET /finance/dues": ok([paidDue, dueSummary]),
    "GET /finance/dues/mine": ok([]),
    "GET /finance/dues/d1": ok(partial),
    "PATCH /finance/dues/d2": ok({ ...paidDue, name: "Cuota de octubre (2)", members: [] }),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  const note = "Ya tiene pagos: solo podés cambiar el nombre.";

  fireEvent.click(await screen.findByRole("button", { name: "Editar Cuota de octubre" }));
  const amount = screen.getByLabelText("Monto en pesos") as HTMLInputElement;
  const dueDate = screen.getByLabelText("Vencimiento") as HTMLInputElement;
  expect(amount.disabled).toBe(true);
  expect(dueDate.disabled).toBe(true);
  expect((screen.getByLabelText("Nombre") as HTMLInputElement).disabled).toBe(false);
  expect(document.getElementById(amount.getAttribute("aria-describedby") ?? "")?.textContent).toBe(note);
  expect(document.getElementById(dueDate.getAttribute("aria-describedby") ?? "")?.textContent).toBe(note);
  fireEvent.change(screen.getByLabelText("Nombre"), { target: { value: "Cuota de octubre (2)" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar cambios" }));
  await waitFor(() => expect(sentTo(sent, "PATCH /finance/dues/d2")).toHaveLength(1));
  expect(sentTo(sent, "PATCH /finance/dues/d2")[0]?.body).toEqual({ name: "Cuota de octubre (2)", amountCents: 200000, dueDate: "2026-11-30" });

  // A partial payment does not show in the list count: the open detail reveals it.
  fireEvent.click(await screen.findByRole("button", { name: "Ver detalle de Cuota de noviembre" }));
  await screen.findByText("Pagó $ 500,00 de $ 2.000,00");
  fireEvent.click(screen.getByRole("button", { name: "Editar Cuota de noviembre" }));
  expect((screen.getByLabelText("Monto en pesos") as HTMLInputElement).disabled).toBe(true);
  expect((screen.getByLabelText("Vencimiento") as HTMLInputElement).disabled).toBe(true);
  expect(screen.getByText(note)).toBeTruthy();
});

test("CS-9 invalid_date server error marks Vencimiento invalid and moves focus to it", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /finance/dues": ok([]),
    "GET /finance/dues/mine": ok([]),
    "POST /finance/dues": fail(422, "invalid_date"),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nueva cuota" }));
  fireEvent.change(screen.getByLabelText("Nombre"), { target: { value: "Cuota de noviembre" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Vencimiento"), { target: { value: "2026-11-30" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));

  const dueDateInput = screen.getByLabelText("Vencimiento") as HTMLInputElement;
  await waitFor(() => expect(dueDateInput.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(dueDateInput);
});

test("PRESERVE navigation includes Cuotas link and navigates to /cuotas", async () => {
  mockApi({
    "GET /auth/me": ok(me()),
    "GET /finance/dues/mine": ok([myDue]),
  });
  window.history.replaceState(null, "", "/");
  render(<App />);
  const nav = await screen.findByRole("navigation", { name: "Principal" });
  const link = within(nav).getByRole("link", { name: "Cuotas" });
  expect(link.getAttribute("href")).toBe("/cuotas");
  fireEvent.click(link);
  expect(await screen.findByRole("heading", { name: "Mis cuotas" })).toBeTruthy();
});

const officerMe = () => me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" });

const openPayment = async () => {
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Ver detalle de Cuota de noviembre" }));
  fireEvent.click(await screen.findByRole("button", { name: "Registrar pago de Beto Ficticio" }));
  return screen.getByLabelText("Monto en pesos") as HTMLInputElement;
};

test("R3 officer registers a partial payment and the detail shows Pagó $ 500,00 de $ 2.000,00", async () => {
  const partial = {
    ...dueDetail,
    members: [dueDetail.members[0], { ...dueDetail.members[1], paidCents: 50000 }],
  };
  const sent = mockApi({
    "GET /auth/me": ok(officerMe()),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([]),
    "GET /finance/dues/d1": [ok(dueDetail), ok(partial)],
    "POST /finance/dues/d1/payments": ok({ id: "p1", dueId: "d1", rosterMemberId: "r2", entryId: "e1", amountCents: 50000, date: "2026-11-13" }, 201),
  });
  const amount = await openPayment();
  expect(screen.getByRole("heading", { name: "Registrar pago de Beto Ficticio" })).toBeTruthy();
  expect(amount.value).toBe("2000");
  fireEvent.change(amount, { target: { value: "500" } });
  fireEvent.change(screen.getByLabelText("Fecha del pago"), { target: { value: "2026-11-13" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar pago" }));

  expect(await screen.findByText("Pagó $ 500,00 de $ 2.000,00")).toBeTruthy();
  expect(screen.getByText("Se registró el pago.")).toBeTruthy();
  expect(sentTo(sent, "POST /finance/dues/d1/payments")).toHaveLength(1);
  expect(sentTo(sent, "POST /finance/dues/d1/payments")[0]?.body).toEqual({ rosterMemberId: "r2", date: "2026-11-13", amountCents: 50000 });
  expect(screen.queryByLabelText("Monto en pesos")).toBeNull();
});

test("R4 overpayment marks Monto en pesos invalid and moves focus to it", async () => {
  mockApi({
    "GET /auth/me": ok(officerMe()),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([]),
    "GET /finance/dues/d1": ok(dueDetail),
    "POST /finance/dues/d1/payments": fail(422, "overpayment"),
  });
  const amount = await openPayment();
  fireEvent.change(amount, { target: { value: "2500" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar pago" }));
  expect(await screen.findByText("El pago supera lo que falta pagar de la cuota.")).toBeTruthy();
  await waitFor(() => expect(document.activeElement).toBe(amount));
  expect(amount.getAttribute("aria-invalid")).toBe("true");
});

test("R9 readers get the CSV link and only officers get Registrar pago", async () => {
  for (const [role, canPay] of [["officer", true], ["school_admin", false]] as const) {
    mockApi({
      "GET /auth/me": ok(role === "officer" ? officerMe() : me({ role, mfa: "satisfied" })),
      "GET /finance/dues": ok([dueSummary]),
      "GET /finance/dues/mine": ok([]),
      "GET /finance/dues/d1": ok(dueDetail),
    });
    window.history.replaceState(null, "", "/cuotas");
    const { unmount } = render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Ver detalle de Cuota de noviembre" }));
    const link = (await screen.findByRole("link", { name: "Descargar CSV" })) as HTMLAnchorElement;
    expect(link.getAttribute("href")).toBe("/api/finance/dues/d1/members.csv");
    expect(link.hasAttribute("download")).toBe(true);
    expect(screen.queryByRole("button", { name: "Registrar pago de Beto Ficticio" }) !== null).toBe(canPay);
    unmount();
  }
});

test("R10 student sees what they paid and what is left in Mis cuotas", async () => {
  mockApi({
    "GET /auth/me": ok(me()),
    "GET /finance/dues/mine": ok([{ ...myDue, paidCents: 50000 }, { ...myDue, id: "d2", name: "Cuota de octubre", status: "paid", paidCents: 200000 }]),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  expect(await screen.findByText("Pagaste $ 500,00 de $ 2.000,00, te falta $ 1.500,00 — vence el 30/11/2026")).toBeTruthy();
  expect(screen.getByText("Pagada $ 2.000,00 — vencía el 30/11/2026")).toBeTruthy();
});

test("CS-9 after recording a payment, focus moves to the status message and never stays on body", async () => {
  const officer = me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" });
  const summary = { id: "d-ver", name: "Cuota de prueba", amountCents: 100000, dueDate: "2026-11-30", createdAt: "2026-11-10T10:00:00Z", owingCount: 1, totalCount: 1 };
  const before = { ...summary, members: [{ rosterMemberId: "m-ver", fullName: "Alumno Uno", status: "owing", paidCents: 0 }] };
  const after = { ...summary, owingCount: 0, members: [{ rosterMemberId: "m-ver", fullName: "Alumno Uno", status: "paid", paidCents: 100000 }] };
  mockApi({
    "GET /auth/me": ok(officer),
    "GET /finance/dues": ok([summary]),
    "GET /finance/dues/mine": ok([]),
    "GET /finance/dues/d-ver": [ok(before), ok(after)],
    "POST /finance/dues/d-ver/payments": ok({ id: "p-ver", dueId: "d-ver", rosterMemberId: "m-ver", entryId: "e-ver", amountCents: 100000, date: "2026-11-13" }, 201),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Ver detalle de Cuota de prueba" }));
  fireEvent.click(await screen.findByRole("button", { name: "Registrar pago de Alumno Uno" }));
  const save = screen.getByRole("button", { name: "Guardar pago" });
  save.focus();
  fireEvent.click(save);
  const status = (await screen.findByText("Se registró el pago.")).closest(".form-status") as HTMLElement;
  await waitFor(() => expect(document.activeElement).toBe(status));
});

test("CS-9 after creating, editing or deleting a due, focus moves to the status message and never stays on body", async () => {
  mockApi({
    "GET /auth/me": ok(officerMe()),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([]),
    "POST /finance/dues": ok(dueDetail, 201),
    "PATCH /finance/dues/d1": ok(dueDetail),
    "DELETE /finance/dues/d1": noContent(),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);

  fireEvent.click(await screen.findByRole("button", { name: "Nueva cuota" }));
  fireEvent.change(screen.getByLabelText("Nombre"), { target: { value: "Cuota de noviembre" } });
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "2000" } });
  fireEvent.change(screen.getByLabelText("Vencimiento"), { target: { value: "2026-11-30" } });
  const create = screen.getByRole("button", { name: "Guardar" });
  create.focus();
  fireEvent.click(create);
  const created = (await screen.findByText("Se creó la cuota.")).closest(".form-status") as HTMLElement;
  await waitFor(() => expect(document.activeElement).toBe(created));

  fireEvent.click(screen.getByRole("button", { name: "Editar Cuota de noviembre" }));
  const edit = screen.getByRole("button", { name: "Guardar cambios" });
  edit.focus();
  fireEvent.click(edit);
  const edited = (await screen.findByText("Se actualizó la cuota.")).closest(".form-status") as HTMLElement;
  await waitFor(() => expect(document.activeElement).toBe(edited));

  fireEvent.click(screen.getByRole("button", { name: "Borrar Cuota de noviembre" }));
  const confirm = within(await screen.findByRole("dialog")).getByRole("button", { name: "Confirmar borrado" });
  confirm.focus();
  fireEvent.click(confirm);
  const deleted = (await screen.findByText("Se borró la cuota.")).closest(".form-status") as HTMLElement;
  await waitFor(() => expect(document.activeElement).toBe(deleted));
});

test("CS-9 when deleting a due fails, focus moves to the error message and never stays on body", async () => {
  mockApi({
    "GET /auth/me": ok(officerMe()),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([]),
    "DELETE /finance/dues/d1": fail(409, "due_frozen"),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Borrar Cuota de noviembre" }));
  const confirm = within(await screen.findByRole("dialog")).getByRole("button", { name: "Confirmar borrado" });
  confirm.focus();
  fireEvent.click(confirm);
  const status = (await screen.findByText(/ya tiene pagos o exenciones/i)).closest(".form-status") as HTMLElement;
  await waitFor(() => expect(document.activeElement).toBe(status));
});

test("#130 R1 R2 Cuotas accepts 1.234,56 in Nueva cuota and Registrar pago and rejects ambiguous amounts without posting", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(officerMe()),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([]),
    "GET /finance/dues/d1": ok(dueDetail),
    "POST /finance/dues": ok(dueDetail, 201),
    "POST /finance/dues/d1/payments": ok({ id: "p1", dueId: "d1", rosterMemberId: "r2", entryId: "e1", amountCents: 123456, date: "2026-11-13" }, 201),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Nueva cuota" }));
  fireEvent.change(screen.getByLabelText("Nombre"), { target: { value: "Cuota de noviembre" } });
  fireEvent.change(screen.getByLabelText("Vencimiento"), { target: { value: "2026-11-30" } });
  const amount = screen.getByLabelText("Monto en pesos");
  for (const raw of ["1.2345", "12,345", "1.23.4", "1,234", "-5", "0", "abc", ""]) {
    fireEvent.change(amount, { target: { value: raw } });
    fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
    await waitFor(() => expect(amount.getAttribute("aria-invalid")).toBe("true"));
    expect(document.getElementById(amount.getAttribute("aria-describedby") ?? "")?.textContent).toBe("Ingresá un monto positivo con hasta dos decimales.");
  }
  expect(sentTo(sent, "POST /finance/dues")).toHaveLength(0);
  fireEvent.change(amount, { target: { value: "1.234,56" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar" }));
  await waitFor(() => expect(sentTo(sent, "POST /finance/dues")).toHaveLength(1));
  expect(sentTo(sent, "POST /finance/dues")[0]?.body).toEqual({ name: "Cuota de noviembre", amountCents: 123456, dueDate: "2026-11-30" });

  fireEvent.click(await screen.findByRole("button", { name: "Ver detalle de Cuota de noviembre" }));
  fireEvent.click(await screen.findByRole("button", { name: "Registrar pago de Beto Ficticio" }));
  fireEvent.change(screen.getByLabelText("Monto en pesos"), { target: { value: "1.234,56" } });
  fireEvent.change(screen.getByLabelText("Fecha del pago"), { target: { value: "2026-11-13" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar pago" }));
  await waitFor(() => expect(sentTo(sent, "POST /finance/dues/d1/payments")).toHaveLength(1));
  expect(sentTo(sent, "POST /finance/dues/d1/payments")[0]?.body).toEqual({ rosterMemberId: "r2", date: "2026-11-13", amountCents: 123456 });
});

const march = { ...dueSummary, name: "Cuota de marzo", dueDate: "2026-03-10" };
const myMarch = { ...myDue, name: "Cuota de marzo", dueDate: "2026-03-10" };
/** 22:00 on 20/11/2026 in Argentina, already 21/11 in UTC. */
const pinArgentinaEvening = () => { vi.useFakeTimers({ toFake: ["Date"] }); vi.setSystemTime(new Date("2026-11-21T01:00:00Z")); };
const sectionOf = (heading: string) => screen.getByRole("heading", { name: heading }).closest("section")!;

test("#135 R4 an unpaid due past its date says Vencida — venció el 10/03/2026 in Mis cuotas and in the commission list", async () => {
  pinArgentinaEvening();
  try {
    mockApi({
      "GET /auth/me": ok(officerMe()),
      "GET /finance/dues": ok([march]),
      "GET /finance/dues/d1": ok({ ...dueDetail, name: "Cuota de marzo", dueDate: "2026-03-10" }),
      "GET /finance/dues/mine": ok([myMarch, { ...myMarch, id: "d2", name: "Cuota de abril", paidCents: 50000 }]),
    });
    window.history.replaceState(null, "", "/cuotas");
    render(<App />);
    expect(await screen.findByText("Debés $ 2.000,00 — Vencida — venció el 10/03/2026")).toBeTruthy();
    expect(screen.getByText("Pagaste $ 500,00 de $ 2.000,00, te falta $ 1.500,00 — Vencida — venció el 10/03/2026")).toBeTruthy();
    await waitFor(() => expect(sectionOf("Listado de cuotas").textContent).toContain("Cuota de marzo — $ 2.000,00 — Vencida — venció el 10/03/2026"));
    fireEvent.click(screen.getByRole("button", { name: "Ver detalle de Cuota de marzo" }));
    expect(await screen.findByText(/\$ 2\.000,00 — Vencida — venció el 10\/03\/2026 · 0 de 2 pagaron/)).toBeTruthy();
  } finally {
    vi.useRealTimers();
  }
});

test("#135 R5 a paid or exempt due, or one due today, never says Vencida", async () => {
  pinArgentinaEvening();
  try {
    mockApi({
      "GET /auth/me": ok(officerMe()),
      "GET /finance/dues": ok([
        { ...march, owingCount: 0 },
        { ...dueSummary, id: "d3", name: "Cuota de hoy", dueDate: "2026-11-20" },
      ]),
      "GET /finance/dues/mine": ok([
        { ...myMarch, status: "paid", paidCents: 200000 },
        { ...myMarch, id: "d2", name: "Cuota de abril", status: "exempt" },
        { ...myMarch, id: "d3", name: "Cuota de hoy", dueDate: "2026-11-20" },
      ]),
    });
    window.history.replaceState(null, "", "/cuotas");
    render(<App />);
    expect(await screen.findByText("Pagada $ 2.000,00 — vencía el 10/03/2026")).toBeTruthy();
    expect(screen.getByText("Exento")).toBeTruthy();
    expect(screen.getByText("Debés $ 2.000,00 — vence el 20/11/2026")).toBeTruthy();
    await waitFor(() => expect(sectionOf("Listado de cuotas").textContent).toContain("Cuota de hoy — $ 2.000,00 — vence el 20/11/2026"));
    expect(sectionOf("Listado de cuotas").textContent).toContain("Cuota de marzo — $ 2.000,00 — vence el 10/03/2026");
    expect(screen.queryByText(/Vencida/)).toBeNull();
  } finally {
    vi.useRealTimers();
  }
});

test("#135 R6 a student sees only their own Vencida and never the commission list", async () => {
  pinArgentinaEvening();
  try {
    const sent = mockApi({
      "GET /auth/me": ok(me()),
      "GET /finance/dues": ok([{ ...march, id: "d9", name: "Cuota de otro" }]),
      "GET /finance/dues/mine": ok([myMarch]),
    });
    window.history.replaceState(null, "", "/cuotas");
    render(<App />);
    expect(await screen.findByText("Debés $ 2.000,00 — Vencida — venció el 10/03/2026")).toBeTruthy();
    expect(screen.getAllByText(/Vencida/)).toHaveLength(1);
    expect(screen.queryByText(/Cuota de otro/)).toBeNull();
    expect(screen.queryByRole("heading", { name: "Listado de cuotas" })).toBeNull();
    expect(sentTo(sent, "GET /finance/dues")).toHaveLength(0);
  } finally {
    vi.useRealTimers();
  }
});
