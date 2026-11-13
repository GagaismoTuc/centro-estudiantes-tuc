// CSV built in the browser (example file) and text printed from API data. A cell that
// starts with = + - @ tab or CR is a formula in spreadsheet apps; the apostrophe makes
// it plain text (OWASP "CSV injection").

const FORMULA_START = /^[=+\-@\t\r]/;

/** Prefixes text that a spreadsheet would run as a formula with an apostrophe. */
export function neutralizeFormula(value: string): string {
  return FORMULA_START.test(value) ? `'${value}` : value;
}

function csvCell(value: string): string {
  const safe = neutralizeFormula(value);
  return /[",;\r\n]/.test(safe) ? `"${safe.replaceAll('"', '""')}"` : safe;
}

/** RFC 4180 rows (CRLF line ends), every cell neutralized. */
export function toCsv(rows: string[][]): string {
  return rows.map((row) => row.map(csvCell).join(",") + "\r\n").join("");
}
