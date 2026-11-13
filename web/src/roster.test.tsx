import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { toCsv } from "./lib/csv.ts";
import { fail, me, mockApi, ok, sentTo, type Reply } from "./test/api.ts";

const admin = me({ role: "school_admin", mfa: "satisfied" });
const members = [
  { id: "m1", fullName: "Ana Gómez", documentNumber: "40111222", hasActiveCode: false, hasAccount: false },
  { id: "m2", fullName: "Bruno Díaz", documentNumber: "41222333", hasActiveCode: true, hasAccount: false },
  { id: "m3", fullName: "Carla Ruiz", documentNumber: "42333444", hasActiveCode: false, hasAccount: true },
];
const issued = (memberId: string, fullName: string, documentNumber: string, code: string) => ({
  memberId,
  fullName,
  documentNumber,
  code,
  expiresAt: "2026-12-13T15:00:00Z",
});

afterEach(() => vi.restoreAllMocks());

async function openRoster(routes: Record<string, Reply | Reply[]> = {}) {
  const sent = mockApi({ "GET /auth/me": ok(admin), "GET /roster": ok(members), ...routes });
  window.history.replaceState(null, "", "/padron");
  render(<App />);
  await screen.findByRole("table", { name: "Alumnos del padrón" });
  return sent;
}

function liveText(): string {
  return Array.from(document.querySelectorAll("[aria-live], [role=status]"))
    .map((el) => el.textContent ?? "")
    .join(" ");
}

function rowOf(name: string): HTMLElement {
  const table = screen.getByRole("table", { name: "Alumnos del padrón" });
  return within(table).getByRole("rowheader", { name }).closest("tr") as HTMLElement;
}

function stat(label: string): string | null | undefined {
  return screen.getByText(label, { selector: "dt" }).nextElementSibling?.textContent;
}

const csv = "full_name,document_number\nDiego Paz,43444555\n";
function upload(file: File) {
  fireEvent.change(screen.getByLabelText("Archivo CSV"), { target: { files: [file] } });
}

describe("roster (/padron)", () => {
  test("shows the counts and one row per member", async () => {
    await openRoster();
    expect(screen.getByRole("heading", { level: 1, name: "Padrón" })).toBeTruthy();
    expect([stat("Alumnos"), stat("Con cuenta"), stat("Con código activo")]).toEqual(["3", "1", "1"]);
    expect(screen.getAllByRole("row")).toHaveLength(4);
    expect(within(rowOf("Bruno Díaz")).getByText("41222333")).toBeTruthy();
    expect(within(rowOf("Bruno Díaz")).getByText("Código activo")).toBeTruthy();
    expect(within(rowOf("Carla Ruiz")).getByText("Con cuenta")).toBeTruthy();
  });

  test("mfa_required reloads the session and sends the admin to two-factor verification", async () => {
    mockApi({
      "GET /auth/me": [ok(admin), ok(me({ role: "school_admin", mfa: "pending" }))],
      "GET /roster": fail(403, "mfa_required"),
    });
    window.history.replaceState(null, "", "/padron");
    render(<App />);
    await waitFor(() => expect(window.location.pathname).toBe("/seguridad/verificar"));
  });

  test("filters by name without accents or by DNI with dots", async () => {
    await openRoster();
    const filter = screen.getByLabelText("Buscar por nombre o DNI");
    fireEvent.change(filter, { target: { value: "gomez" } });
    expect(screen.getAllByRole("rowheader").map((th) => th.textContent)).toEqual(["Ana Gómez"]);
    fireEvent.change(filter, { target: { value: "41.222" } });
    expect(screen.getAllByRole("rowheader").map((th) => th.textContent)).toEqual(["Bruno Díaz"]);
    expect(liveText()).toContain("1 de 3 alumnos");
  });
});

describe("roster import", () => {
  test("uploads the CSV, reports inserted and skipped rows and reloads the list", async () => {
    const sent = await openRoster({
      "POST /roster/import": ok({ inserted: 2, skipped: 1 }),
      "GET /roster": [ok(members), ok([...members, { ...members[0], id: "m4", fullName: "Diego Paz" }])],
    });
    expect(screen.getByText(/full_name,document_number/)).toBeTruthy();
    upload(new File([csv], "padron.csv", { type: "text/csv" }));

    await waitFor(() =>
      expect(liveText()).toContain("Se cargaron 2 alumnos nuevos. 1 ya estaba en el padrón y se omitió."),
    );
    expect(await screen.findByRole("rowheader", { name: "Diego Paz" })).toBeTruthy();
    const [request] = sentTo(sent, "POST /roster/import");
    expect(request?.contentType).toBe("text/csv");
    expect(request?.body).toBe(csv);
    expect(sentTo(sent, "GET /roster")).toHaveLength(2);
  });

  test("a dropped file reported as application/vnd.ms-excel is accepted and sent as text/csv", async () => {
    const sent = await openRoster({ "POST /roster/import": ok({ inserted: 1, skipped: 0 }) });
    const zone = screen.getByText(/Arrastrá el archivo/).closest(".dropzone") as HTMLElement;
    const file = new File([csv], "padron.CSV", { type: "application/vnd.ms-excel" });
    fireEvent.drop(zone, { dataTransfer: { files: [file], types: ["Files"] } });

    await waitFor(() => expect(liveText()).toContain("Se cargó 1 alumno nuevo."));
    expect(sentTo(sent, "POST /roster/import")[0]?.contentType).toBe("text/csv");
  });

  test("a file that is not CSV is rejected without uploading", async () => {
    const sent = await openRoster();
    upload(new File(["x"], "foto.png", { type: "image/png" }));
    await waitFor(() => expect(liveText()).toContain("El archivo tiene que ser CSV."));
    expect(sentTo(sent, "POST /roster/import")).toHaveLength(0);
  });

  test("invalid rows are listed as «Línea N: motivo» in Spanish", async () => {
    await openRoster({
      "POST /roster/import": {
        status: 422,
        body: {
          error: "invalid_rows",
          rows: [
            { line: 2, reason: "document_number must have 7 to 9 digits" },
            { line: 5, reason: "duplicate document_number (line 3)" },
            { line: 7, reason: "something new" },
          ],
        },
      },
    });
    upload(new File([csv], "padron.csv", { type: "text/csv" }));

    const list = await screen.findByRole("list", { name: "Filas con errores" });
    expect(within(list).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
      "Línea 2: el DNI tiene que tener de 7 a 9 números",
      "Línea 5: el DNI está repetido (ya aparece en la línea 3)",
      "Línea 7: revisá los datos de esta fila",
    ]);
    expect(liveText()).toContain("El archivo tiene filas con errores. No se cargó nada.");
  });

  test.each([
    [413, "body_too_large", "El archivo es demasiado grande. El máximo es 1 MB."],
    [415, "unsupported_media_type", "El archivo tiene que ser CSV."],
    [422, "too_many_rows", "El archivo tiene más de 5000 filas. Dividilo en partes."],
  ])("%s %s shows a clear message", async (status, error, message) => {
    await openRoster({ "POST /roster/import": fail(status, error) });
    upload(new File([csv], "padron.csv", { type: "text/csv" }));
    await waitFor(() => expect(liveText()).toContain(message));
  });

  test("the example CSV is generated in the browser with the expected header", async () => {
    await openRoster();
    const link = screen.getByRole("link", { name: "Descargar un ejemplo" });
    expect(link.getAttribute("download")).toBe("padron-ejemplo.csv");
    const href = link.getAttribute("href") ?? "";
    expect(href.startsWith("data:text/csv;charset=utf-8,")).toBe(true);
    const decoded = decodeURIComponent(href.split(",").slice(1).join(","));
    expect(decoded.startsWith(String.fromCharCode(0xfeff) + "full_name,document_number" + String.fromCharCode(13, 10))).toBe(true);
  });

  test("generated CSV cells starting with = + - @ tab or CR get an apostrophe", () => {
    expect(toCsv([["=1+2", "+54", "-3", "@x", "\tA", "\rB", "Pérez, Ana", "ok"]])).toBe(
      `'=1+2,'+54,'-3,'@x,'\tA,"'\rB","Pérez, Ana",ok\r\n`,
    );
  });
});

describe("issuing access codes", () => {
  test("a member without a code gets one right away; one with an active code needs confirmation", async () => {
    const sent = await openRoster({
      "POST /roster/m1/access-code": ok({ code: "AAAA-BBBB-CCCC", expiresAt: "2026-12-13T15:00:00Z" }, 201),
      "POST /roster/m2/access-code": ok({ code: "DDDD-EEEE-FFFF", expiresAt: "2026-12-13T15:00:00Z" }, 201),
    });
    fireEvent.click(within(rowOf("Ana Gómez")).getByRole("button", { name: "Generar código" }));
    const panel = await screen.findByRole("region", { name: "Códigos generados" });
    expect(within(panel).getByText("AAAA-BBBB-CCCC")).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();

    fireEvent.click(within(rowOf("Bruno Díaz")).getByRole("button", { name: "Generar código" }));
    const dialog = screen.getByRole("dialog", { name: "¿Generar un código nuevo para Bruno Díaz?" });
    expect(within(dialog).getByText(/El código anterior va a dejar de funcionar/)).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(sentTo(sent, "POST /roster/m2/access-code")).toHaveLength(0);

    fireEvent.click(within(rowOf("Bruno Díaz")).getByRole("button", { name: "Generar código" }));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Generar código nuevo" }));
    expect(await within(panel).findByText("DDDD-EEEE-FFFF")).toBeTruthy();
    expect(sentTo(sent, "POST /roster/m2/access-code")).toHaveLength(1);
    expect(sentTo(sent, "POST /roster/m1/access-code")).toHaveLength(1);
  });

  test("a member who already has an account cannot get a code, and the row says why", async () => {
    await openRoster();
    const row = rowOf("Carla Ruiz");
    expect((within(row).getByRole("button", { name: "Generar código" }) as HTMLButtonElement).disabled).toBe(true);
    expect(within(row).getByText("Ya creó su cuenta: no necesita código.")).toBeTruthy();
  });

  test("bulk codes are shown once, with copy, print cards and formula-safe names", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    const print = vi.spyOn(window, "print").mockImplementation(() => {});
    const sent = await openRoster({
      "POST /roster/access-codes": ok(
        [
          issued("m1", "Ana Gómez", "40111222", "AAAA-BBBB-CCCC"),
          issued("m9", '=HYPERLINK("x")', "45666777", "GGGG-HHHH-JJJJ"),
        ],
        201,
      ),
    });
    fireEvent.click(screen.getByRole("button", { name: "Generar códigos faltantes" }));

    const panel = await screen.findByRole("region", { name: "Códigos generados" });
    expect(within(panel).getByText(/No se van a volver a mostrar/)).toBeTruthy();
    expect(within(panel).getByText('=HYPERLINK("x")')).toBeTruthy();
    expect(within(panel).getAllByText("13 de diciembre de 2026")).toHaveLength(2);
    expect(within(rowOf("Ana Gómez")).getByText("Código activo")).toBeTruthy();

    fireEvent.click(within(panel).getByRole("button", { name: "Copiar código de Ana Gómez" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("AAAA-BBBB-CCCC"));

    fireEvent.click(within(panel).getByRole("button", { name: "Imprimir" }));
    expect(print).toHaveBeenCalledTimes(1);
    const cards = Array.from(document.querySelectorAll(".print-sheet .print-card")).map((c) => c.textContent);
    expect(cards).toHaveLength(2);
    expect(cards[0]).toContain("Ana Gómez");
    expect(cards[0]).toContain("AAAA-BBBB-CCCC");
    expect(cards[0]).toContain("13 de diciembre de 2026");
    expect(cards[0]).toContain(`${window.location.origin}/alta`);
    expect(cards[1]).toContain(`'=HYPERLINK("x")`);

    fireEvent.click(within(panel).getByRole("button", { name: "Ocultar códigos" }));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Ocultar" }));
    expect(screen.queryByRole("region", { name: "Códigos generados" })).toBeNull();
    expect(document.querySelector(".print-sheet")).toBeNull();
    expect(sentTo(sent, "POST /roster/access-codes")).toHaveLength(1);
  });

  test("leaving the page asks for confirmation only while codes are visible", async () => {
    await openRoster({
      "POST /roster/access-codes": ok([issued("m1", "Ana Gómez", "40111222", "AAAA-BBBB-CCCC")], 201),
    });
    const leave = () => {
      const event = new Event("beforeunload", { cancelable: true });
      window.dispatchEvent(event);
      return event.defaultPrevented;
    };
    expect(leave()).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: "Generar códigos faltantes" }));
    await screen.findByRole("region", { name: "Códigos generados" });
    expect(leave()).toBe(true);

    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    const nav = screen.getByRole("navigation", { name: "Principal" });
    fireEvent.click(within(nav).getByRole("link", { name: "Inicio" }));
    expect(window.location.pathname).toBe("/padron");
    fireEvent.click(within(nav).getByRole("link", { name: "Inicio" }));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(leave()).toBe(false);
  });
});
