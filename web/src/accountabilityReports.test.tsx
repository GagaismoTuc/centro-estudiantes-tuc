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

function fillAndPreview(term: string, from: string, to: string) {
  fireEvent.change(screen.getByLabelText(/^Gestión/), { target: { value: term } });
  fireEvent.change(screen.getByLabelText("Desde"), { target: { value: from } });
  fireEvent.change(screen.getByLabelText("Hasta"), { target: { value: to } });
  fireEvent.click(screen.getByRole("button", { name: "Vista previa" }));
}

test("R9 lists the published reports with period, Gestión and who published, and opens one with its CSV", async () => {
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
  expect(within(view).getByText("Cuota de noviembre").nextElementSibling?.textContent).toBe("312 de 470 pagaron");
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

  fireEvent.click(within(preview).getByRole("button", { name: "Publicar" }));
  await screen.findByText("Rendición publicada.");
  expect(sentTo(sent, "POST /finance/accountability-reports").map((s) => s.body)).toEqual([{ from: "2026-03-01", to: "2026-11-30", term: "Gestión 2026" }]);
  const list = screen.getByRole("region", { name: "Rendiciones publicadas" });
  expect((await within(list).findByText("Gestión 2026")).closest("li")!.textContent).toContain("Publicada por Ana Ficticia");
  expect(screen.queryByRole("region", { name: "Vista previa" })).toBeNull();
});

test("R9 editing the form after the preview discards it", async () => {
  mockApi({ "GET /auth/me": ok(officer), "GET /finance/accountability-reports": ok([]), "GET /finance/report": ok(fundReport) });
  open();
  await screen.findByRole("region", { name: "Publicar una rendición" });
  fillAndPreview("Gestión 2026", "2026-03-01", "2026-11-30");
  await screen.findByRole("region", { name: "Vista previa" });
  fireEvent.change(screen.getByLabelText("Desde"), { target: { value: "2026-04-01" } });
  expect(screen.queryByRole("region", { name: "Vista previa" })).toBeNull();
});

test("R5 Publicar is disabled while sending, so a double click publishes once", async () => {
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
  fireEvent.click(publish);
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
  mockApi({
    "GET /auth/me": ok(officer),
    "GET /finance/accountability-reports": ok([]),
    "GET /finance/report": ok(fundReport),
    "POST /finance/accountability-reports": fail(422, "future_date"),
  });
  open();
  await screen.findByRole("region", { name: "Publicar una rendición" });
  fillAndPreview("Gestión 2026", "2026-03-01", "2026-11-30");
  fireEvent.click(within(await screen.findByRole("region", { name: "Vista previa" })).getByRole("button", { name: "Publicar" }));
  await screen.findByText("La fecha no puede ser futura.");
  const to = screen.getByLabelText("Hasta");
  await waitFor(() => expect(document.activeElement).toBe(to));
  expect(to.getAttribute("aria-invalid")).toBe("true");
});
