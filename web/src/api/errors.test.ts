import { describe, expect, test } from "vitest";
import contract from "../../../openapi/openapi.yaml?raw";
import { errorMessage } from "./errors.ts";

// Error codes the contract names in backticks (`invalid_credentials`), minus the
// backticked words that are states, token purposes or CSV columns, plus the two
// one-word codes the snake_case pattern cannot tell apart from prose.
const notErrorCodes = ["enrollment_required", "not_required", "reset_password", "sign_in", "verify_email", "document_number", "full_name"];
const codes = [
  ...new Set([...contract.matchAll(/`([a-z]+(?:_[a-z]+)+)`/g)].map((m) => m[1] as string)),
  "unauthenticated",
  "forbidden",
].filter((code) => !notErrorCodes.includes(code));

describe("errorMessage", () => {
  test("the contract lists the codes the UI must translate", () => {
    expect(codes).toEqual(
      expect.arrayContaining(["invalid_credentials", "too_many_attempts", "email_in_use", "mfa_required", "weak_password"]),
    );
  });

  test.each(codes)("%s has its own Spanish message that never shows the raw code", (code) => {
    const message = errorMessage(code);
    expect(message).not.toContain(code);
    expect(message).not.toBe(errorMessage("something_unknown"));
  });

  test("unknown codes and server errors fall back to a generic message", () => {
    expect(errorMessage("something_unknown")).toBe("Algo salió mal de nuestro lado. Probá de nuevo en un rato.");
  });

  test("too many attempts says to wait about 15 minutes (A13)", () => {
    expect(errorMessage("too_many_attempts")).toContain("15 minutos");
  });

  test("a wrong recovery code has its own copy that does not mention the app (A7)", () => {
    expect(errorMessage("invalid_code", "mfa-recovery")).not.toContain("app");
    expect(errorMessage("invalid_code", "mfa-recovery")).not.toBe(errorMessage("invalid_code", "mfa"));
  });
});

describe("shared error messages", () => {
  test("preserves contact and finance messages for shared error codes", () => {
    expect(errorMessage("description_required")).toBe("Escrib\u00ed una descripci\u00f3n para el precio de referencia.");
    expect(errorMessage("description_required", "finance")).toBe("Escrib\u00ed una descripci\u00f3n.");
    expect(errorMessage("invalid_amount")).toBe("Ingres\u00e1 un monto mayor a cero.");
    expect(errorMessage("invalid_amount", "finance")).toBe("Ingres\u00e1 un monto positivo en centavos enteros.");
    expect(errorMessage("invalid_date")).toBe("Ingres\u00e1 una fecha v\u00e1lida.");
    expect(errorMessage("invalid_date", "finance")).toBe("Eleg\u00ed una fecha v\u00e1lida.");
  });
});
