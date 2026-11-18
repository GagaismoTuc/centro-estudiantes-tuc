import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.useRealTimers());

const sale = { id: "e1", categoryId: "c1", direction: "income", date: "2026-03-10", amountCents: 200000, description: "Venta de tortas", recordedBy: "Ana Ficticia", recordedAt: "2026-03-10T12:00:00Z" };
const rent = { id: "e2", categoryId: "c3", direction: "expense", date: "2026-04-02", amountCents: 1500000, description: "Alquiler del salón", recordedBy: "Ana Ficticia", recordedAt: "2026-04-02T12:00:00Z" };
const report = {
  from: "2026-03-01", to: "2026-11-30",
  openingCents: 1000000, closingCents: -300000, incomeCents: 200000, expenseCents: -1500000,
  totals: [
    { categoryId: "c1", name: "Ventas", direction: "income", totalCents: 200000 },
    { categoryId: "c3", name: "Alquileres", direction: "expense", totalCents: -1500000 },
  ],
  entries: [sale, rent],
};

function chooseRange(from: string, to: string) {
  fireEvent.change(screen.getByLabelText("Desde"), { target: { value: from } });
  fireEvent.change(screen.getByLabelText("Hasta"), { target: { value: to } });
  fireEvent.click(screen.getByRole("button", { name: "Ver informe" }));
}

test("S9 a Student picks a range and reads the report with its CSV link", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-11-17T15:00:00Z"));
  const sent = mockApi({ "GET /auth/me": ok(me()), "GET /finance/report": ok(report) });
  const queries: string[] = [];
  const handler = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation(async (input, init) => { queries.push(new URL((input as Request).url).search); return handler(input, init); });
  window.history.replaceState(null, "", "/caja/informe"); render(<App />);
  const from = await screen.findByLabelText("Desde");
  const to = screen.getByLabelText("Hasta");
  expect((from as HTMLInputElement).value).toBe("2026-11-01");
  expect((to as HTMLInputElement).value).toBe("2026-11-17");
  await waitFor(() => expect(queries).toContain("?from=2026-11-01&to=2026-11-17"));

  chooseRange("2026-03-01", "2026-11-30");
  await waitFor(() => expect(sentTo(sent, "GET /finance/report")).toHaveLength(2));
  expect(queries).toContain("?from=2026-03-01&to=2026-11-30");
  const summary = await screen.findByRole("region", { name: "Saldos" });
  expect(within(summary).getByText("Saldo inicial").nextElementSibling?.textContent).toBe("$ 10.000,00");
  expect(within(summary).getByText("Saldo final").nextElementSibling?.textContent).toBe("$ -3.000,00");
  const movements = screen.getByRole("region", { name: "Movimientos del rango" });
  expect(within(movements).getByText("Venta de tortas")).toBeTruthy();
  expect(within(movements).getByText(/Ventas.*10\/03\/2026/)).toBeTruthy();
  const totals = screen.getByRole("region", { name: "Totales por categoría" });
  const income = within(totals).getByRole("list", { name: "Ingresos" });
  const expense = within(totals).getByRole("list", { name: "Gastos" });
  expect(within(income).getByText("Ventas").nextElementSibling?.textContent).toBe("$ 2.000,00");
  expect(within(expense).getByText("Alquileres").nextElementSibling?.textContent).toBe("$ -15.000,00");
  const csv = screen.getByRole("link", { name: "Descargar CSV" });
  expect(csv.getAttribute("href")).toBe("/api/finance/report.csv?from=2026-03-01&to=2026-11-30");
  expect(csv.hasAttribute("download")).toBe(true);
});

test("S9 Desde after Hasta is announced in Spanish, focused and sends nothing", async () => {
  const sent = mockApi({ "GET /auth/me": ok(me()), "GET /finance/report": ok(report) });
  window.history.replaceState(null, "", "/caja/informe"); render(<App />);
  await screen.findByLabelText("Desde");
  await waitFor(() => expect(sentTo(sent, "GET /finance/report")).toHaveLength(1));
  chooseRange("2026-11-30", "2026-03-01");
  const to = screen.getByLabelText("Hasta");
  await waitFor(() => expect(to.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(to);
  expect(document.getElementById(to.getAttribute("aria-describedby") ?? "")?.textContent).toBe("La fecha «Hasta» no puede ser anterior a «Desde».");
  expect(screen.queryByRole("link", { name: "Descargar CSV" })).toBeNull();
  fireEvent.change(screen.getByLabelText("Desde"), { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "Ver informe" }));
  const from = screen.getByLabelText("Desde");
  await waitFor(() => expect(from.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(from);
  expect(document.getElementById(from.getAttribute("aria-describedby") ?? "")?.textContent).toBe("Elegí la fecha de inicio.");
  expect(sentTo(sent, "GET /finance/report")).toHaveLength(1);
});

test("S9 the server's invalid_range is shown in Spanish and marks the dates", async () => {
  mockApi({ "GET /auth/me": ok(me()), "GET /finance/report": [ok(report), fail(422, "invalid_range")] });
  window.history.replaceState(null, "", "/caja/informe"); render(<App />);
  await screen.findByRole("region", { name: "Saldos" });
  chooseRange("2026-03-01", "2026-11-30");
  const message = await screen.findByText("Revisá las fechas: elegí las dos y que la de inicio no sea posterior a la de fin.");
  expect(message.closest("[aria-live]")).toBeTruthy();
  const from = screen.getByLabelText("Desde");
  await waitFor(() => expect(document.activeElement).toBe(from));
  expect(from.getAttribute("aria-invalid")).toBe("true");
  expect(screen.getByLabelText("Hasta").getAttribute("aria-invalid")).toBe("true");
});

test("S9 report amounts beyond 2^53 cents render exactly", async () => {
  mockApi({ "GET /auth/me": ok(me()) });
  const handler = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    if (new URL((input as Request).url).pathname !== "/api/finance/report") return handler(input, init);
    const body = '{"from":"2026-03-01","to":"2026-11-30","openingCents":9007199254740993,"closingCents":9007199254740995,"incomeCents":9007199254740997,"expenseCents":-9007199254740999,'
      + '"totals":[{"categoryId":"c1","name":"Ventas","direction":"income","totalCents":9007199254740997},{"categoryId":"c3","name":"Alquileres","direction":"expense","totalCents":-9007199254740999}],"entries":[]}';
    return new Response(body, { status: 200, headers: { "Content-Type": "application/json" } });
  });
  window.history.replaceState(null, "", "/caja/informe"); render(<App />);
  const summary = await screen.findByRole("region", { name: "Saldos" });
  expect(within(summary).getByText("Saldo inicial").nextElementSibling?.textContent).toBe("$ 90.071.992.547.409,93");
  expect(within(summary).getByText("Saldo final").nextElementSibling?.textContent).toBe("$ 90.071.992.547.409,95");
  expect(within(summary).getByText("Total de ingresos").nextElementSibling?.textContent).toBe("$ 90.071.992.547.409,97");
  expect(within(summary).getByText("Total de gastos").nextElementSibling?.textContent).toBe("$ -90.071.992.547.409,99");
  const totals = screen.getByRole("region", { name: "Totales por categoría" });
  expect(within(within(totals).getByRole("list", { name: "Ingresos" })).getByText("Ventas").nextElementSibling?.textContent).toBe("$ 90.071.992.547.409,97");
  expect(within(within(totals).getByRole("list", { name: "Gastos" })).getByText("Alquileres").nextElementSibling?.textContent).toBe("$ -90.071.992.547.409,99");
});

test("S9 a range without movements shows zero totals and says so", async () => {
  mockApi({ "GET /auth/me": ok(me()), "GET /finance/report": ok({ ...report, closingCents: 1000000, incomeCents: 0, expenseCents: 0, totals: [], entries: [] }) });
  window.history.replaceState(null, "", "/caja/informe"); render(<App />);
  expect(await screen.findByText("No hay movimientos entre esas fechas.")).toBeTruthy();
  const summary = screen.getByRole("region", { name: "Saldos" });
  expect(within(summary).getByText("Saldo inicial").nextElementSibling?.textContent).toBe("$ 10.000,00");
  expect(within(summary).getByText("Total de ingresos").nextElementSibling?.textContent).toBe("$ 0,00");
  expect(within(summary).getByText("Total de gastos").nextElementSibling?.textContent).toBe("$ 0,00");
  expect(within(summary).getByText("Saldo final").nextElementSibling?.textContent).toBe("$ 10.000,00");
});
