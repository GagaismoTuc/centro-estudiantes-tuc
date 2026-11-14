import { describe, expect, test } from "vitest";
import { encodeQr } from "./encode.ts";
import { vectors } from "./vectors.fixture.ts";

const rows = (modules: boolean[][]) => modules.map((row) => row.map((dark) => (dark ? "1" : "0")).join(""));

describe("encodeQr", () => {
  for (const vector of vectors) {
    test(`matches the reference matrix for the ${vector.name}`, () => {
      const symbol = encodeQr(vector.text);
      expect({ version: symbol.version, mask: symbol.mask }).toEqual({ version: vector.version, mask: vector.mask });
      expect(rows(symbol.modules)).toEqual(vector.rows);
    });
  }

  test("a text longer than version 40-M holds throws a clear error", () => {
    expect(encodeQr("a".repeat(2331)).version).toBe(40);
    expect(() => encodeQr("a".repeat(2332))).toThrow(
      new RangeError("QR: the text takes 2332 bytes and version 40-M holds at most 2331 bytes in byte mode"),
    );
  });
});
