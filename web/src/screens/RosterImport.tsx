import { useId, useState, type ChangeEvent, type DragEvent } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { FormStatus, type Status } from "../components/Form.tsx";
import { Icon } from "../components/Icon.tsx";
import { countDataLines, toCsv } from "../lib/csv.ts";

type RowError = components["schemas"]["RowError"];

const MAX_BYTES = 1024 * 1024;
const MAX_NAME = 200;

// The BOM makes Excel open the accents right; the API accepts it.
const EXAMPLE_HREF =
  "data:text/csv;charset=utf-8," +
  encodeURIComponent(
    String.fromCharCode(0xfeff) +
      toCsv([
        ["full_name", "document_number"],
        ["María José Pérez", "40.111.222"],
        ["Juan Ignacio López", "41222333"],
      ]),
  );

// One Spanish explanation per API RowError.code; the English reason is never shown.
const reasons: Record<RowError["code"], (row: RowError) => string> = {
  header_invalid: () => "la primera línea tiene que ser full_name,document_number",
  field_count: () => "tiene que tener 2 columnas: nombre y DNI",
  encoding_invalid: () => "el archivo tiene que estar guardado como «CSV UTF-8»",
  csv_malformed: () => "el CSV está mal armado; revisá las comillas",
  full_name_required: () => "falta el nombre",
  full_name_too_long: () => `el nombre puede tener hasta ${MAX_NAME} caracteres`,
  full_name_invalid: () => "el nombre tiene que estar en una sola línea",
  full_name_invisible: () => "el nombre tiene caracteres invisibles; escribilo de nuevo",
  document_number_required: () => "falta el DNI",
  document_number_spaces: () => "el DNI no puede tener espacios (escribilo 42345678 o 42.345.678)",
  document_number_format: () =>
    "el DNI solo puede tener números, con o sin puntos de miles (42345678 o 42.345.678)",
  document_number_length: () => "el DNI tiene que tener de 7 a 9 números",
  document_number_duplicate: ({ duplicateOfLine }) =>
    duplicateOfLine ? `el DNI está repetido (ya aparece en la línea ${duplicateOfLine})` : "el DNI está repetido",
};

function describeRow(row: RowError): string {
  const explain = Object.hasOwn(reasons, row.code) ? reasons[row.code] : null;
  const text = explain ? explain(row) : "revisá los datos de esta fila";
  return `${row.line > 0 ? `Línea ${row.line}` : "Archivo"}: ${text}`;
}

function rowsOf(body: unknown): RowError[] {
  const rows = typeof body === "object" && body !== null && "rows" in body ? body.rows : null;
  return Array.isArray(rows) ? (rows as RowError[]) : [];
}

const plural = (n: number, one: string, many: string) => (n === 1 ? `1 ${one}` : `${n} ${many}`);

/**
 * "El archivo tiene N filas con errores y M filas bien.", counted from the file itself; null when
 * the API could not read the rows one by one (bad header, broken CSV), so no count is honest.
 */
function rowCount(rows: RowError[], dataLines: number | null): string | null {
  if (dataLines === null || rows.some((r) => r.line <= 1 || r.code === "csv_malformed")) return null;
  const bad = new Set(rows.map((r) => r.line)).size;
  const fine = Math.max(0, dataLines - bad);
  return `El archivo tiene ${plural(bad, "fila", "filas")} con errores y ${plural(fine, "fila", "filas")} bien.`;
}

function summary({ inserted, skipped }: { inserted: number; skipped: number }): string {
  if (inserted === 0 && skipped > 0) {
    return skipped === 1
      ? "No había alumnos nuevos: el alumno ya estaba en el padrón."
      : `No había alumnos nuevos: los ${skipped} ya estaban en el padrón.`;
  }
  const added = inserted === 1 ? "Se cargó 1 alumno nuevo." : `Se cargaron ${inserted} alumnos nuevos.`;
  if (skipped === 0) return added;
  return skipped === 1
    ? `${added} 1 ya estaba en el padrón y se omitió.`
    : `${added} ${skipped} ya estaban en el padrón y se omitieron.`;
}

/** Excel on Windows often reports CSV files as application/vnd.ms-excel: trust the extension. */
function isCsv(file: File): boolean {
  return /\.csv$/i.test(file.name) || file.type === "text/csv";
}

type Rejection = { rows: RowError[]; count: string | null };

type Props = {
  /** Called when an upload starts (the page clears its own older messages). */
  onStart: () => void;
  onImported: () => void;
  /** Spanish message for a failed request (also reloads the session when needed). */
  describeFailure: (code: string) => string;
};

export function RosterImport({ onStart, onImported, describeFailure }: Props) {
  const id = useId();
  const [status, setStatus] = useState<Status>(null);
  const [rejection, setRejection] = useState<Rejection | null>(null);
  const [fileName, setFileName] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [dragging, setDragging] = useState(false);

  async function upload(file: File) {
    setRejection(null);
    setFileName(file.name);
    onStart();
    if (!isCsv(file)) return setStatus({ tone: "error", text: errorMessage("unsupported_media_type") });
    if (file.size > MAX_BYTES) return setStatus({ tone: "error", text: errorMessage("body_too_large") });
    setPending(true);
    setStatus({ tone: "info", text: `Cargando ${file.name}…` });
    // The raw bytes, whatever MIME type the browser guessed: decoding here would hide a
    // non-UTF-8 file that the API must reject.
    const bytes = new Uint8Array(await file.arrayBuffer());
    // Only a BOM or blank lines is as empty as zero bytes; the API would call it a bad header.
    if (new TextDecoder().decode(bytes).trim() === "") {
      setPending(false);
      return setStatus({ tone: "error", text: "El archivo está vacío." });
    }
    const result = await call(
      apiClient.POST("/roster/import", {
        body: "",
        bodySerializer: () => bytes,
        headers: { "Content-Type": "text/csv" },
      }),
    );
    setPending(false);
    if (result.ok) {
      setStatus({ tone: "ok", text: summary(result.data) });
      onImported();
      return;
    }
    if (result.code !== "invalid_rows") return setStatus({ tone: "error", text: describeFailure(result.code) });
    const rows = rowsOf(result.body);
    setStatus(null);
    setRejection({ rows, count: rowCount(rows, countDataLines(new TextDecoder().decode(bytes))) });
  }

  function onChange(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = ""; // choosing the same file again (after fixing it) fires change
    if (file) void upload(file);
  }

  function onDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setDragging(false);
    const file = event.dataTransfer.files[0];
    if (file && !pending) void upload(file);
  }

  return (
    <section className="panel" aria-labelledby={`${id}-title`} aria-busy={pending || undefined}>
      <h2 id={`${id}-title`}>Importar alumnos</h2>
      <p className="muted small">
        La primera línea tiene que ser <code>full_name,document_number</code> (con <code>;</code> también sirve, como lo
        guarda Excel). Después, una fila por alumno con su nombre y DNI, con o sin puntos. Guardalo como «CSV UTF-8». Los
        DNI que ya están en el padrón se omiten.{" "}
        <a href={EXAMPLE_HREF} download="padron-ejemplo.csv">
          Descargar un ejemplo
        </a>
      </p>
      <div
        className="dropzone"
        data-dragging={dragging || undefined}
        onDragOver={(event) => {
          event.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
      >
        <Icon name="upload" />
        <p className="dropzone__title">Archivo CSV</p>
        <p className="muted small" id={`${id}-hint`}>
          Arrastrá el archivo acá o elegilo desde tu equipo. Máximo 1 MB y 5000 filas.
        </p>
        {/* The real input stays focusable and labelled; only its English native look is hidden. */}
        <input
          id={id}
          className="file-input"
          type="file"
          accept=".csv,text/csv,application/vnd.ms-excel"
          disabled={pending}
          aria-describedby={`${id}-name ${id}-hint`}
          onChange={onChange}
        />
        <label htmlFor={id} className="button button--secondary file-button">
          Elegir archivo
        </label>
        <p className="file-name small" id={`${id}-name`}>
          {fileName ?? "Ningún archivo elegido"}
        </p>
      </div>
      <FormStatus status={status} />
      {rejection && (
        <div className="import-result" role="alert">
          <p className="import-result__title">
            <Icon name="alert" />
            <span>No se cargó ningún alumno</span>
          </p>
          <p>
            {rejection.count ?? "El archivo tiene errores."} Para que el padrón no quede cargado a medias, se carga
            solo cuando todas las filas están bien.
          </p>
          {rejection.rows.length > 0 && (
            <ul className="row-errors" aria-label="Filas con errores">
              {rejection.rows.map((row) => (
                <li key={`${row.line}-${row.code}`}>{describeRow(row)}</li>
              ))}
            </ul>
          )}
          <p>
            <strong>Corregí esas líneas y volvé a subir el archivo.</strong>
          </p>
        </div>
      )}
    </section>
  );
}
