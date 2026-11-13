import { useId, useState, type ChangeEvent, type DragEvent, type Ref } from "react";
import { call } from "../api/call.ts";
import { apiClient } from "../api/client.ts";
import { errorMessage } from "../api/errors.ts";
import type { components } from "../api/schema.gen.ts";
import { FormStatus, type Status } from "../components/Form.tsx";
import { Icon } from "../components/Icon.tsx";
import { toCsv } from "../lib/csv.ts";

type RowError = components["schemas"]["RowError"];

const MAX_BYTES = 1024 * 1024;

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

// The API explains rejected rows in English (openapi RowError.reason is free text).
const reasons: [RegExp, (m: RegExpMatchArray) => string][] = [
  [/^header must be/, () => "la primera línea tiene que ser full_name,document_number"],
  [/^expected 2 fields$/, () => "tiene que tener 2 columnas: nombre y DNI"],
  [/^file must be saved as CSV UTF-8$/, () => "el archivo tiene que estar guardado como «CSV UTF-8»"],
  [/^full_name is required$/, () => "falta el nombre"],
  [/^full_name must have at most (\d+) characters$/, (m) => `el nombre puede tener hasta ${m[1]} caracteres`],
  [/^full_name must be a single line of text$/, () => "el nombre tiene que estar en una sola línea"],
  [/^full_name contains invisible characters$/, () => "el nombre tiene caracteres invisibles; escribilo de nuevo"],
  [/^document_number must have 7 to 9 digits$/, () => "el DNI tiene que tener de 7 a 9 números"],
  [/^duplicate document_number \(line (\d+)\)$/, (m) => `el DNI está repetido (ya aparece en la línea ${m[1]})`],
  [/^malformed CSV$/, () => "el CSV está mal armado; revisá las comillas"],
];

function describeRow({ line, reason }: RowError): string {
  for (const [pattern, text] of reasons) {
    const match = reason.match(pattern);
    if (match) return `${line > 0 ? `Línea ${line}` : "Archivo"}: ${text(match)}`;
  }
  return `${line > 0 ? `Línea ${line}` : "Archivo"}: revisá los datos de esta fila`;
}

function rowsOf(body: unknown): RowError[] {
  const rows = typeof body === "object" && body !== null && "rows" in body ? body.rows : null;
  return Array.isArray(rows) ? (rows as RowError[]) : [];
}

function summary({ inserted, skipped }: { inserted: number; skipped: number }): string {
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

type Props = {
  inputRef: Ref<HTMLInputElement>;
  onImported: () => void;
  /** Spanish message for a failed request (also reloads the session when needed). */
  describeFailure: (code: string) => string;
};

export function RosterImport({ inputRef, onImported, describeFailure }: Props) {
  const id = useId();
  const [status, setStatus] = useState<Status>(null);
  const [rows, setRows] = useState<RowError[]>([]);
  const [pending, setPending] = useState(false);
  const [dragging, setDragging] = useState(false);

  async function upload(file: File) {
    setRows([]);
    if (!isCsv(file)) return setStatus({ tone: "error", text: errorMessage("unsupported_media_type") });
    if (file.size > MAX_BYTES) return setStatus({ tone: "error", text: errorMessage("body_too_large") });
    setPending(true);
    setStatus({ tone: "info", text: `Cargando ${file.name}…` });
    // The raw bytes, whatever MIME type the browser guessed: decoding here would hide a
    // non-UTF-8 file that the API must reject.
    const bytes = new Uint8Array(await file.arrayBuffer());
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
    setStatus({ tone: "error", text: describeFailure(result.code) });
    if (result.code === "invalid_rows") setRows(rowsOf(result.body));
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
        <label htmlFor={id}>Archivo CSV</label>
        <p className="muted small">Arrastrá el archivo acá o elegilo desde tu equipo. Máximo 1 MB y 5000 filas.</p>
        <input
          ref={inputRef}
          id={id}
          type="file"
          accept=".csv,text/csv,application/vnd.ms-excel"
          disabled={pending}
          onChange={onChange}
        />
      </div>
      <FormStatus status={status} />
      {rows.length > 0 && (
        <ul className="row-errors" aria-label="Filas con errores">
          {rows.map((row) => (
            <li key={`${row.line}-${row.reason}`}>{describeRow(row)}</li>
          ))}
        </ul>
      )}
    </section>
  );
}
