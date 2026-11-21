import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.useRealTimers());

const published = { id: "r1", from: "2025-03-01", to: "2025-11-30", term: "Gestión 2025", publishedBy: "Dario Ficticio", publishedAt: "2026-03-02T15:00:00Z" };
const fundReport = {
  from: "2026-03-01", to: "2026-11-30",
  openingCents: 100000, closingCents: 130000, incomeCents: 50000, expenseCents: -20000,
  totals: [], entries: [{ id: "e1" }, { id: "e2" }],
};
const copy = {
  id: "r2", from: "2026-03-01", to: "2026-11-30", term: "Gestión 2026", publishedBy: "Ana Ficticia", publishedAt: "2026-11-30T15:00:00Z",
  openingCents: 100000, closingCents: 130000,
  totals: [{ categoryId: "c1", name: "Ventas", direction: "income", totalCents: 50000 }],
  dues: [{ dueId: "d1", name: "Cuota de noviembre", dueDate: "2026-11-15", amountCents: 200000, paidCount: 312, totalCount: 470 }],
  entries: [{ id: "e1", date: "2026-03-01", direction: "income", categoryId: "c1", category: "Ventas", description: "Kiosco de marzo", amountCents: 50000, recordedBy: "Ana Ficticia", reversed: false }],
};
const officer = me({ role: "officer", mfa: "satisfied" });

function open() {
  window.history.replaceState(null, "", "/caja/rendiciones");
  render(<App />);
}

/** Pins Argentina's today, so «Hasta» 30/11/2026 is not in the future. */
function today(instant = "2026-11-30T15:00:00Z") {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date(instant));
}

/** Publicar in the preview, then the confirmation's own Publicar. */
async function publishConfirmed() {
  fireEvent.click(within(await screen.findByRole("region", { name: "Vista previa" })).getByRole("button", { name: "Publicar" }));
  fireEvent.click(within(await screen.findByRole("dialog", { name: "Publicar la rendición" })).getByRole("button", { name: "Publicar" }));
}

function fillAndPreview(term: string, from: string, to: string) {
  fireEvent.change(screen.getByLabelText(/^Gestión/), { target: { value: term } });
  fireEvent.change(screen.getByLabelText("Desde"), { target: { value: from } });
  fireEvent.change(screen.getByLabelText("Hasta"), { target: { value: to } });
  fireEvent.click(screen.getByRole("button", { name: "Vista previa" }));
}

test("R9 lists the published reports with period, Gestión and who published, and opens one with its CSV and its paid count up to the end", async () => {
  mockApi({ "GET /auth/me": ok(me()), "GET /finance/accountability-reports": ok([published]), "GET /finance/accountability-reports/r1": ok({ ...copy, ...published }) });
  open();
  const list = await screen.findByRole("region", { name: "Rendiciones publicadas" });
  const item = (await within(list).findByText("Gestión 2025")).closest("li")!;
  expect(item.textContent).toContain("Del 01/03/2025 al 30/11/2025");
  expect(item.textContent).toContain("Publicada por Dario Ficticio");
  expect(within(item).getByRole("link", { name: "Descargar CSV" }).getAttribute("href")).toBe("/api/finance/accountability-reports/r1.csv");
  expect(screen.queryByRole("region", { name: "Publicar una rendición" })).toBeNull();

  fireEvent.click(within(item).getByRole("button", { name: "Ver" }));
  const view = await screen.findByRole("region", { name: "Gestión 2025" });
  expect(within(view).getByText("Cuota de noviembre").nextElementSibling?.textContent).toBe("312 de 470 pagaron al 30/11/2025");
  expect(within(view).getByText("Saldo final").nextElementSibling?.textContent).toBe("$ 1.300,00");
  expect(within(view).getByText("Kiosco de marzo")).toBeTruthy();
});

test("R9 an Officer previews the period with the notice before publishing it", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-11-30T15:00:00Z"));
  const sent = mockApi({
    "GET /auth/me": ok(officer),
    "GET /finance/accountability-reports": [ok([]), ok([copy])],
    "GET /finance/report": ok(fundReport),
    "POST /finance/accountability-reports": ok(copy, 201),
  });
  open();
  await screen.findByRole("region", { name: "Publicar una rendición" });
  expect(screen.queryByText("Una vez publicada no se puede cambiar.")).toBeNull();
  fillAndPreview("  Gestión 2026 ", "2026-03-01", "2026-11-30");
  const preview = await screen.findByRole("region", { name: "Vista previa" });
  expect(within(preview).getByText("Una vez publicada no se puede cambiar.")).toBeTruthy();
  expect(within(preview).getByText("Saldo final").nextElementSibling?.textContent).toBe("$ 1.300,00");
  expect(preview.textContent).toContain("Gestión 2026 — Del 01/03/2026 al 30/11/2026");
  expect(sentTo(sent, "POST /finance/accountability-reports")).toHaveLength(0);

  await publishConfirmed();
  await screen.findByText("Rendición publicada.");
  expect(sentTo(sent, "POST /finance/accountability-reports").map((s) => s.body)).toEqual([{ from: "2026-03-01", to: "2026-11-30", term: "Gestión 2026" }]);
  const list = screen.getByRole("region", { name: "Rendiciones publicadas" });
  expect((await within(list).findByText("Gestión 2026")).closest("li")!.textContent).toContain("Publicada por Ana Ficticia");
  expect(screen.queryByRole("region", { name: "Vista previa" })).toBeNull();
});

test("R9 editing the form after the preview discards it", async () => {
  today();
  mockApi({ "GET /auth/me": ok(officer), "GET /finance/accountability-reports": ok([]), "GET /finance/report": ok(fundReport) });
  open();
  await screen.findByRole("region", { name: "Publicar una rendición" });
  fillAndPreview("Gestión 2026", "2026-03-01", "2026-11-30");
  await screen.findByRole("region", { name: "Vista previa" });
  fireEvent.change(screen.getByLabelText("Desde"), { target: { value: "2026-04-01" } });
  expect(screen.queryByRole("region", { name: "Vista previa" })).toBeNull();
});

test("R5 Publicar is disabled while sending, so a double click publishes once", async () => {
  today();
  let release: (value: Response) => void = () => {};
  const sent = mockApi({ "GET /auth/me": ok(officer), "GET /finance/accountability-reports": ok([]), "GET /finance/report": ok(fundReport) });
  const routed = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const request = input as Request;
    if (request.method !== "POST") return routed(input, init);
    sent.push({ method: "POST", path: new URL(request.url).pathname.replace(/^\/api/, ""), body: JSON.parse(await request.text()), contentType: null, href: "" });
    return new Promise<Response>((resolve) => { release = resolve; });
  });
  open();
  await screen.findByRole("region", { name: "Publicar una rendición" });
  fillAndPreview("Gestión 2026", "2026-03-01", "2026-11-30");
  const publish = within(await screen.findByRole("region", { name: "Vista previa" })).getByRole("button", { name: "Publicar" });
  await publishConfirmed();
  await waitFor(() => expect((publish as HTMLButtonElement).disabled).toBe(true));
  fireEvent.click(publish);
  expect(sentTo(sent, "POST /finance/accountability-reports")).toHaveLength(1);
  release(new Response(JSON.stringify(copy), { status: 201, headers: { "Content-Type": "application/json" } }));
  await screen.findByText("Rendición publicada.");
  expect(sentTo(sent, "POST /finance/accountability-reports")).toHaveLength(1);
});

test("R9 a blank Gestión is announced, marked and focused, and nothing is sent", async () => {
  const sent = mockApi({ "GET /auth/me": ok(officer), "GET /finance/accountability-reports": ok([]) });
  open();
  await screen.findByRole("region", { name: "Publicar una rendición" });
  fillAndPreview("   ", "2026-03-01", "2026-11-30");
  const term = screen.getByLabelText(/^Gestión/);
  await waitFor(() => expect(document.activeElement).toBe(term));
  expect(term.getAttribute("aria-invalid")).toBe("true");
  expect(screen.getByText("Escribí la gestión, por ejemplo «Gestión 2026».")).toBeTruthy();
  expect(sentTo(sent, "GET /finance/report")).toHaveLength(0);
});

test("R9 a future end the server rejects is announced on Hasta", async () => {
  today();
  mockApi({
    "GET /auth/me": ok(officer),
    "GET /finance/accountability-reports": ok([]),
    "GET /finance/report": ok(fundReport),
    "POST /finance/accountability-reports": fail(422, "future_date"),
  });
  open();
  await screen.findByRole("region", { name: "Publicar una rendición" });
  fillAndPreview("Gestión 2026", "2026-03-01", "2026-11-30");
  await publishConfirmed();
  await screen.findByText("La fecha no puede ser futura.");
  const to = screen.getByLabelText("Hasta");
  await waitFor(() => expect(document.activeElement).toBe(to));
  expect(to.getAttribute("aria-invalid")).toBe("true");
});

test("R2 the preview warns about a Rendición of the same Gestión and period, and Publicar stays allowed", async () => {
  today();
  const earlier = { id: "r3", from: "2026-03-01", to: "2026-11-30", term: "Gestión 2026", publishedBy: "Ana Ficticia", publishedAt: "2026-11-21T01:30:00Z" };
  const sent = mockApi({
    "GET /auth/me": ok(officer),
    "GET /finance/accountability-reports": ok([published, earlier]),
    "GET /finance/report": [ok({ ...fundReport, from: "2026-04-01" }), ok(fundReport)],
    "POST /finance/accountability-reports": ok(copy, 201),
  });
  open();
  await screen.findByText("Del 01/03/2026 al 30/11/2026");
  const warning = "Ya hay una rendición de esta gestión y período, publicada el 20/11/2026.";
  fillAndPreview("Gestión 2026", "2026-04-01", "2026-11-30");
  await screen.findByRole("region", { name: "Vista previa" });
  expect(screen.queryByText(warning)).toBeNull();

  fillAndPreview("Gestión 2026", "2026-03-01", "2026-11-30");
  const preview = await screen.findByRole("region", { name: "Vista previa" });
  expect(within(preview).getByText(warning)).toBeTruthy();
  await publishConfirmed();
  await screen.findByText("Rendición publicada.");
  expect(sentTo(sent, "POST /finance/accountability-reports").map((s) => s.body)).toEqual([{ from: "2026-03-01", to: "2026-11-30", term: "Gestión 2026" }]);
});

test("R3 Publicar asks in a ConfirmDialog that repeats the notice; Cancelar publishes nothing", async () => {
  today();
  const sent = mockApi({
    "GET /auth/me": ok(officer),
    "GET /finance/accountability-reports": ok([]),
    "GET /finance/report": ok(fundReport),
    "POST /finance/accountability-reports": ok(copy, 201),
  });
  open();
  await screen.findByRole("region", { name: "Publicar una rendición" });
  fillAndPreview("Gestión 2026", "2026-03-01", "2026-11-30");
  const publish = within(await screen.findByRole("region", { name: "Vista previa" })).getByRole("button", { name: "Publicar" });
  publish.focus();
  fireEvent.click(publish);
  let dialog = await screen.findByRole("dialog", { name: "Publicar la rendición" });
  expect(within(dialog).getByText("Una vez publicada no se puede cambiar.")).toBeTruthy();
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(publish));
  expect(sentTo(sent, "POST /finance/accountability-reports")).toHaveLength(0);
  expect(screen.getByRole("region", { name: "Vista previa" })).toBeTruthy();

  fireEvent.click(publish);
  dialog = await screen.findByRole("dialog", { name: "Publicar la rendición" });
  fireEvent.click(within(dialog).getByRole("button", { name: "Publicar" }));
  const message = (await screen.findByText("Rendición publicada.")).closest("[aria-live]");
  await waitFor(() => expect(document.activeElement).toBe(message));
  expect(sentTo(sent, "POST /finance/accountability-reports")).toHaveLength(1);
});

test("R4 a Hasta of tomorrow is a field error before asking for the preview", async () => {
  today("2026-11-20T15:00:00Z");
  const sent = mockApi({ "GET /auth/me": ok(officer), "GET /finance/accountability-reports": ok([]), "GET /finance/report": ok(fundReport) });
  open();
  await screen.findByRole("region", { name: "Publicar una rendición" });
  fillAndPreview("Gestión 2026", "2026-03-01", "2026-11-21");
  const to = screen.getByLabelText("Hasta");
  await waitFor(() => expect(document.activeElement).toBe(to));
  expect(to.getAttribute("aria-invalid")).toBe("true");
  expect(document.getElementById(to.getAttribute("aria-describedby") ?? "")?.textContent).toBe("La fecha no puede ser futura.");
  expect(sentTo(sent, "GET /finance/report")).toHaveLength(0);
  expect(screen.queryByRole("region", { name: "Vista previa" })).toBeNull();

  fillAndPreview("Gestión 2026", "2026-03-01", "2026-11-20");
  await screen.findByRole("region", { name: "Vista previa" });
  expect(sentTo(sent, "GET /finance/report")).toHaveLength(1);
});

test("R4 downloading a published PDF reports converter failures", async () => {
  mockApi({ "GET /auth/me": ok(me()), "GET /finance/accountability-reports": ok([published]), "GET /finance/accountability-reports/r1": ok({ ...copy, ...published }), "GET /finance/accountability-reports/r1.pdf": fail(503, "pdf_unavailable") });
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Ver" }));
  const view = await screen.findByRole("region", { name: "Gesti\u00f3n 2025" });
  fireEvent.click(within(view).getByRole("button", { name: "Descargar PDF" }));
  const message = await within(view).findByText("No pudimos generar el PDF. Prob\u00e1 de nuevo en unos minutos.");
  await waitFor(() => expect(document.activeElement).toBe(message.closest("[aria-live]")));
});


test("R1 downloads the PDF of the opened accountability report", async () => {
 const sent = mockApi({ "GET /auth/me": ok(me()), "GET /finance/accountability-reports": ok([published]), "GET /finance/accountability-reports/r1": ok({ ...copy, ...published }), "GET /finance/accountability-reports/r1.pdf": ok("pdf") });
 const handler = vi.mocked(fetch).getMockImplementation()!;
 vi.mocked(fetch).mockImplementation(async (input, init) => {
   const response = await handler(input, init);
   if (new URL((input as Request).url).pathname !== "/api/finance/accountability-reports/r1.pdf") return response;
   return new Response("%PDF-1.4", { headers: { "Content-Type": "application/pdf", "Content-Disposition": 'attachment; filename="rendicion-de-cuentas-gestion-2025-2025-03-01-a-2025-11-30.pdf"' } });
 });
 const create = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:report");
 const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
 let downloaded: HTMLAnchorElement | undefined;
 const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) { downloaded = this; });
 open(); fireEvent.click(await screen.findByRole("button", { name: "Ver" }));
 const view = await screen.findByRole("region", { name: "Gesti\u00f3n 2025" });
 fireEvent.click(within(view).getByRole("button", { name: "Descargar PDF" }));
 await waitFor(() => expect(click).toHaveBeenCalled());
 expect(sentTo(sent, "GET /finance/accountability-reports/r1.pdf")).toHaveLength(1);
 expect(downloaded?.download).toBe("rendicion-de-cuentas-gestion-2025-2025-03-01-a-2025-11-30.pdf");
 expect(revoke).toHaveBeenCalledWith("blob:report");
 create.mockRestore(); revoke.mockRestore(); click.mockRestore();
});


test("switching reports clears the previous PDF error", async () => {
  mockApi({ "GET /auth/me": ok(me()), "GET /finance/accountability-reports": ok([published, copy]), "GET /finance/accountability-reports/r1": ok({ ...copy, ...published }), "GET /finance/accountability-reports/r2": ok(copy), "GET /finance/accountability-reports/r1.pdf": fail(503, "pdf_unavailable") });
  open();
  const list = await screen.findByRole("region", { name: "Rendiciones publicadas" });
  const items = await within(list).findAllByRole("listitem");
  fireEvent.click(within(items[0]!).getByRole("button", { name: "Ver" }));
  const first = await screen.findByRole("region", { name: "Gesti\u00f3n 2025" });
  fireEvent.click(within(first).getByRole("button", { name: "Descargar PDF" }));
  await within(first).findByText("No pudimos generar el PDF. Prob\u00e1 de nuevo en unos minutos.");
  fireEvent.click(within(items[1]!).getByRole("button", { name: "Ver" }));
  const second = await screen.findByRole("region", { name: "Gesti\u00f3n 2026" });
  expect(within(second).queryByText("No pudimos generar el PDF. Prob\u00e1 de nuevo en unos minutos.")).toBeNull();
});
