/** Formats whatever the user typed (any case, spaces, hyphens) as XXXX-XXXX-XXXX. */
export function formatAccessCode(raw: string): string {
  const chars = raw.toUpperCase().replace(/[^0-9A-Z]/g, "").slice(0, 12);
  return chars.match(/.{1,4}/g)?.join("-") ?? "";
}

/** Groups a base32 TOTP secret in blocks of four for reading and typing. */
export function groupSecret(secret: string): string {
  return secret.match(/.{1,4}/g)?.join(" ") ?? "";
}
