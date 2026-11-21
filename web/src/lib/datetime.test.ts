import { expect, test } from "vitest";
import { formatDate, formatDateTime } from "./datetime.ts";

// Every source file of web/src, as text, keyed by its path relative to this file.
const sources = import.meta.glob<string>(["../**/*.{ts,tsx}"], { query: "?raw", import: "default", eager: true });

test("R1 a recordedAt shows in Argentina time on a 24-hour clock", () => {
  expect(formatDateTime("2026-11-21T01:45:25Z")).toBe("20/11/2026 22:45");
});

test("R2 a time just after midnight shows as 00, never 24 or 12", () => {
  expect(formatDateTime("2026-11-21T03:05:00Z")).toBe("21/11/2026 00:05");
});

test("R3 a date without time shows as dd/mm/aaaa", () => {
  expect(formatDate("2026-11-20")).toBe("20/11/2026");
});

test("R4 no file of web/src calls toLocaleString or toLocaleTimeString outside lib/datetime.ts", () => {
  expect(Object.keys(sources)).toContain("../screens/Finance.tsx");
  const offenders = Object.entries(sources)
    .filter(([path, text]) => path !== "./datetime.ts" && /\.toLocale(?:Time)?String\s*\(/.test(text))
    .map(([path]) => path);
  expect(offenders).toEqual([]);
});
