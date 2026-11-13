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
});
