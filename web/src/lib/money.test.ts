import { describe, expect, test } from "vitest";
import { formatPesos, parsePesos } from "./money.ts";

describe("parsePesos", () => {
  test("R1 reads a dot followed by exactly three digits as thousands and a comma or a final one-or-two-digit dot as decimals", () => {
    expect(parsePesos("1.234,56")).toBe(123456n);
    expect(parsePesos("1234,56")).toBe(123456n);
    expect(parsePesos("1234.56")).toBe(123456n);
    expect(parsePesos("1.234")).toBe(123400n);
    expect(parsePesos("600.000")).toBe(60000000n);
    expect(parsePesos("1.23")).toBe(123n);
  });

  test("R2 rejects ambiguous, negative, zero, non-numeric and empty amounts", () => {
    for (const raw of ["1.2345", "12,345", "1.23.4", "1,234", "-5", "0", "abc", ""]) {
      expect(parsePesos(raw), raw).toBeNull();
    }
  });

  test("R4 parses amounts up to the int64 maximum exactly and rejects one centavo more", () => {
    expect(parsePesos("92.233.720.368.547.758,07")).toBe(9223372036854775807n);
    expect(parsePesos("92233720368547758,08")).toBeNull();
  });
});

describe("formatPesos", () => {
  test("R3 always shows two decimals, a space after $ and the minus sign before $", () => {
    expect(formatPesos(30000150n)).toBe("$ 300.001,50");
    expect(formatPesos("30000150")).toBe("$ 300.001,50");
    expect(formatPesos(30000150)).toBe("$ 300.001,50");
    expect(formatPesos(-19526988n)).toBe("-$ 195.269,88");
    expect(formatPesos(0)).toBe("$ 0,00");
  });

  test("R4 formats the int64 maximum exactly", () => {
    expect(formatPesos("9223372036854775807")).toBe("$ 92.233.720.368.547.758,07");
  });
});
