import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { toCsv } from "./lib/csv.ts";
import { emulateDialog, fail, me, mockApi, ok, pressEscape, renderStrict, sentTo, settle, type Reply } from "./test/api.ts";

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

/** Texts of the elements an element's aria-describedby points to. */
function describedBy(el: HTMLElement): (string | null | undefined)[] {
  return (el.getAttribute("aria-describedby") ?? "").split(" ").map((id) => document.getElementById(id)?.textContent);
}

function stat(label: string): string | null | undefined {
  return screen.getByText(label, { selector: "dt" }).nextElementSibling?.textContent;
}

const csv = "full_name,document_number\nDiego Paz,43444555\n";
function upload(file: File) {
  fireEvent.change(screen.getByLabelText("Elegir archivo"), { target: { files: [file] } });
}

describe("roster (/padron)", () => {
  test("shows the counts and one row per member", async () => {
    await openRoster();
    expect(screen.getByRole("heading", { level: 1, name: "Padrón" })).toBeTruthy();
    expect([stat("Alumnos"), stat("Con cuenta"), stat("Con código activo")]).toEqual(["3", "1", "1"]);
    expect(screen.getAllByRole("term").map((dt) => dt.textContent)).toEqual(["Alumnos", "Con cuenta", "Con código activo"]);
    expect(screen.getAllByRole("definition").map((dd) => dd.textContent)).toEqual(["3", "1", "1"]);
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

  test("the file picker is in Spanish and shows the chosen file name", async () => {
    await openRoster({ "POST /roster/import": ok({ inserted: 1, skipped: 0 }) });
    const input = screen.getByLabelText("Elegir archivo") as HTMLInputElement;
    expect(input.type).toBe("file");
    expect(screen.getByText("Ningún archivo elegido")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Importar CSV" })).toBeNull();
    upload(new File([csv], "padron.csv", { type: "text/csv" }));
    expect(await screen.findByText("padron.csv")).toBeTruthy();
    expect(describedBy(input)).toContain("padron.csv");
  });

  const rejected = (rows: unknown[]): Reply => ({ status: 422, body: { error: "invalid_rows", rows } });
  const rowTexts = () =>
    within(screen.getByRole("list", { name: "Filas con errores" }))
      .getAllByRole("listitem")
      .map((li) => li.textContent);

  test("a rejected file explains that nothing was loaded on purpose, how many rows are fine and what to do", async () => {
    await openRoster({
      "POST /roster/import": rejected([
        { line: 2, code: "document_number_spaces", reason: "document_number must not contain spaces" },
        { line: 3, code: "full_name_required", reason: "full_name is required" },
        { line: 5, code: "document_number_duplicate", duplicateOfLine: 4, reason: "duplicate document_number (line 4)" },
        { line: 5, code: "document_number_length", reason: "document_number must have 7 to 9 digits" },
        { line: 7, code: "something_new", reason: "something new" },
      ]),
    });
    // 6 data lines (the blank line does not count) with errors on 4 distinct lines: 2 rows are fine.
    const file = ["full_name,document_number", "A,42 345 678", ",40111222", "C,30111222", "D,30111222", "", "E,1", "F,40111223", ""];
    upload(new File([file.join("\r\n")], "padron.csv", { type: "text/csv" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("No se cargó ningún alumno");
    expect(alert.textContent).toContain("El archivo tiene 4 filas con errores y 2 filas bien.");
    expect(alert.textContent).toContain("Para que el padrón no quede cargado a medias");
    expect(alert.textContent).toContain("Corregí esas líneas y volvé a subir el archivo.");
    expect(rowTexts()).toEqual([
      "Línea 2: el DNI no puede tener espacios (escribilo 42345678 o 42.345.678)",
      "Línea 3: falta el nombre",
      "Línea 5: el DNI está repetido (ya aparece en la línea 4)",
      "Línea 5: el DNI tiene que tener de 7 a 9 números",
      "Línea 7: revisá los datos de esta fila",
    ]);
    expect(liveText()).not.toContain("El archivo tiene filas con errores");
  });

  test.each([
    ["header_invalid", 1, "Línea 1: la primera línea tiene que ser full_name,document_number"],
    ["field_count", 2, "Línea 2: tiene que tener 2 columnas: nombre y DNI"],
    ["encoding_invalid", 2, "Línea 2: el archivo tiene que estar guardado como «CSV UTF-8»"],
    ["csv_malformed", 0, "Archivo: el CSV está mal armado; revisá las comillas"],
    ["full_name_too_long", 2, "Línea 2: el nombre puede tener hasta 200 caracteres"],
    ["full_name_invalid", 2, "Línea 2: el nombre tiene que estar en una sola línea"],
    ["full_name_invisible", 2, "Línea 2: el nombre tiene caracteres invisibles; escribilo de nuevo"],
    ["document_number_required", 2, "Línea 2: falta el DNI"],
    ["document_number_format", 2, "Línea 2: el DNI solo puede tener números, con o sin puntos de miles (42345678 o 42.345.678)"],
  ])("row code %s is explained in Spanish", async (code, line, text) => {
    await openRoster({ "POST /roster/import": rejected([{ line, code, reason: "x" }]) });
    upload(new File([csv], "padron.csv", { type: "text/csv" }));
    await screen.findByRole("alert");
    expect(rowTexts()).toEqual([text]);
  });

  test("a bad header does not claim a count of fine rows", async () => {
    await openRoster({
      "POST /roster/import": rejected([{ line: 1, code: "header_invalid", reason: "header must be full_name,document_number" }]),
    });
    upload(new File(["nombre,dni\nAna,40111222\n"], "padron.csv", { type: "text/csv" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("No se cargó ningún alumno");
    expect(alert.textContent).not.toMatch(/\d+ filas? bien/);
  });

  test("the import message goes away when codes are issued", async () => {
    await openRoster({
      "POST /roster/import": ok({ inserted: 1, skipped: 0 }),
      "POST /roster/m1/access-code": ok({ code: "AAAA-BBBB-CCCC", expiresAt: "2026-12-13T15:00:00Z" }, 201),
    });
    upload(new File([csv], "padron.csv", { type: "text/csv" }));
    await waitFor(() => expect(liveText()).toContain("Se cargó 1 alumno nuevo."));
    fireEvent.click(within(rowOf("Ana Gómez")).getByRole("button", { name: "Generar código" }));
    await screen.findByRole("region", { name: "Códigos generados" });
    expect(liveText()).not.toContain("Se cargó 1 alumno nuevo.");
  });

  test("starting a new import clears the previous codes message", async () => {
    await openRoster({
      "POST /roster/import": ok({ inserted: 1, skipped: 0 }),
      "POST /roster/access-codes": ok([issued("m1", "Ana Gómez", "40111222", "AAAA-BBBB-CCCC")], 201),
    });
    fireEvent.click(screen.getByRole("button", { name: "Generar códigos faltantes" }));
    await waitFor(() => expect(liveText()).toContain("Se generó 1 código."));
    upload(new File([csv], "padron.csv", { type: "text/csv" }));
    await waitFor(() => expect(liveText()).toContain("Se cargó 1 alumno nuevo."));
    expect(liveText()).not.toContain("Se generó 1 código.");
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

  test("R5 (#134) an empty file says so and is not sent", async () => {
    const sent = await openRoster();
    for (const content of ["", String.fromCharCode(0xfeff, 10)]) {
      upload(new File([content], "vacio.csv", { type: "text/csv" }));
      await waitFor(() => expect(liveText()).toContain("El archivo está vacío."));
    }
    expect(sentTo(sent, "POST /roster/import")).toHaveLength(0);
  });

  test("R5 (#134) a reimport without new students says none were new", async () => {
    await openRoster({ "POST /roster/import": ok({ inserted: 0, skipped: 2 }) });
    upload(new File([csv], "padron.csv", { type: "text/csv" }));
    await waitFor(() => expect(liveText()).toContain("No había alumnos nuevos: los 2 ya estaban en el padrón."));
    expect(liveText()).not.toContain("Se cargaron 0");
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
    expect(within(rowOf("Ana Gómez")).getByRole("button", { name: "Generar código nuevo" })).toBeTruthy();

    const again = within(rowOf("Bruno Díaz")).getByRole("button", { name: "Generar código nuevo" });
    expect(describedBy(again)).toContain("Reemplaza el código activo.");
    fireEvent.click(again);
    const dialog = screen.getByRole("dialog", { name: "¿Generar un código nuevo para Bruno Díaz?" });
    expect(within(dialog).getByText(/El código anterior va a dejar de funcionar/)).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(sentTo(sent, "POST /roster/m2/access-code")).toHaveLength(0);

    fireEvent.click(within(rowOf("Bruno Díaz")).getByRole("button", { name: "Generar código nuevo" }));
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

  test.each([
    ["Escuela Demo Tucumán", "Escuela Demo Tucumán"],
    [null, "Centro de Estudiantes"],
  ])("print cards name the school %s as %s", async (tenantName, line) => {
    await openRoster({
      "GET /auth/me": ok({ ...admin, tenantName }),
      "POST /roster/access-codes": ok([issued("m1", "Ana Gómez", "40111222", "AAAA-BBBB-CCCC")], 201),
    });
    fireEvent.click(screen.getByRole("button", { name: "Generar códigos faltantes" }));
    await screen.findByRole("region", { name: "Códigos generados" });
    const brand = document.querySelector(".print-sheet .print-card .print-card__brand");
    expect(brand?.textContent).toBe(line);
    expect(brand?.querySelector("bdi") !== null).toBe(tenantName !== null);
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

    // Leaving through the app asks in the in-app dialog (A6).
    const nav = screen.getByRole("navigation", { name: "Principal" });
    fireEvent.click(within(nav).getByRole("link", { name: "Inicio" }));
    const ask = await screen.findByRole("dialog", { name: "¿Salir de esta pantalla?" });
    fireEvent.click(within(ask).getByRole("button", { name: "Cancelar" }));
    expect(window.location.pathname).toBe("/padron");
    fireEvent.click(within(nav).getByRole("link", { name: "Inicio" }));
    const again = await screen.findByRole("dialog", { name: "¿Salir de esta pantalla?" });
    fireEvent.click(within(again).getByRole("button", { name: "Salir igual" }));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(leave()).toBe(false);
  });
});

describe("A6 under StrictMode: «Generar código nuevo» confirmation", () => {
  test("stays open until answered; Esc cancels without issuing", async () => {
    const restoreDialog = emulateDialog();
    try {
      const sent = mockApi({ "GET /auth/me": ok(admin), "GET /roster": ok(members) });
      window.history.replaceState(null, "", "/padron");
      renderStrict(<App />);
      await screen.findByRole("table", { name: "Alumnos del padrón" });
      fireEvent.click(within(rowOf("Bruno Díaz")).getByRole("button", { name: "Generar código nuevo" }));
      await settle();
      const dialog = screen.getByRole("dialog", { name: "¿Generar un código nuevo para Bruno Díaz?" });
      pressEscape(dialog);
      await settle();
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(sentTo(sent, "POST /roster/m2/access-code")).toHaveLength(0);
    } finally {
      restoreDialog();
    }
  });

  test("confirming Generar código nuevo moves focus to the Códigos generados heading", async () => {
    const restoreDialog = emulateDialog();
    try {
      mockApi({
        "GET /auth/me": ok(admin),
        "GET /roster": ok(members),
        "POST /roster/m1/access-code": ok({ code: "AAAA-BBBB-CCCC", expiresAt: "2026-12-13T15:00:00Z" }, 201),
        "POST /roster/m2/access-code": [
          ok({ code: "DDDD-EEEE-FFFF", expiresAt: "2026-12-13T15:00:00Z" }, 201),
          ok({ code: "GGGG-HHHH-JJJJ", expiresAt: "2026-12-13T15:00:00Z" }, 201),
        ],
      });
      window.history.replaceState(null, "", "/padron");
      renderStrict(<App />);
      await screen.findByRole("table", { name: "Alumnos del padrón" });

      fireEvent.click(within(rowOf("Bruno Díaz")).getByRole("button", { name: "Generar código nuevo" }));
      await settle();
      fireEvent.click(
        within(screen.getByRole("dialog", { name: "¿Generar un código nuevo para Bruno Díaz?" })).getByRole("button", {
          name: "Generar código nuevo",
        }),
      );
      const heading = await screen.findByRole("heading", { name: "Códigos generados" });
      expect(heading.getAttribute("tabindex")).toBe("-1");
      await waitFor(() => expect(document.activeElement).toBe(heading));

      // Also when the Códigos generados panel is already mounted on screen.
      fireEvent.click(within(rowOf("Ana Gómez")).getByRole("button", { name: "Generar código" }));
      await waitFor(() => expect(within(rowOf("Ana Gómez")).getByRole("button", { name: "Generar código nuevo" })).toBeTruthy());
      fireEvent.click(within(rowOf("Bruno Díaz")).getByRole("button", { name: "Generar código nuevo" }));
      await settle();
      fireEvent.click(
        within(screen.getByRole("dialog", { name: "¿Generar un código nuevo para Bruno Díaz?" })).getByRole("button", {
          name: "Generar código nuevo",
        }),
      );
      const panel = screen.getByRole("region", { name: "Códigos generados" });
      await waitFor(() => expect(within(panel).getByText("GGGG-HHHH-JJJJ")).toBeTruthy());
      await waitFor(() => expect(document.activeElement).toBe(heading));
    } finally {
      restoreDialog();
    }
  });

  test("cancelling Ocultar códigos keeps the codes visible", async () => {
    const restoreDialog = emulateDialog();
    try {
      await openRoster({
        "POST /roster/m1/access-code": ok({ code: "AAAA-BBBB-CCCC", expiresAt: "2026-12-13T15:00:00Z" }, 201),
      });
      fireEvent.click(within(rowOf("Ana Gómez")).getByRole("button", { name: "Generar código" }));
      const panel = await screen.findByRole("region", { name: "Códigos generados" });
      fireEvent.click(within(panel).getByRole("button", { name: "Ocultar códigos" }));
      await settle();
      const dialog = screen.getByRole("dialog", { name: "¿Ocultar los códigos?" });
      fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
      await settle();
      expect(screen.queryByRole("dialog")).toBeNull();
      const region = screen.getByRole("region", { name: "Códigos generados" });
      expect(region).toBeTruthy();
      expect(within(region).getByText("AAAA-BBBB-CCCC")).toBeTruthy();
    } finally {
      restoreDialog();
    }
  });
});
