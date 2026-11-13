// Spanish messages for every `error` code in openapi/openapi.yaml. The UI never shows raw codes.

const GENERIC = "Algo salió mal de nuestro lado. Probá de nuevo en un rato.";

const messages: Record<string, string> = {
  invalid_credentials: "El mail o la contraseña no coinciden.",
  too_many_attempts: "Hiciste demasiados intentos. Esperá unos minutos y probá de nuevo.",
  bad_request: "No pudimos procesar el pedido. Revisá los datos e intentá otra vez.",
  unauthenticated: "Tu sesión terminó. Ingresá de nuevo.",
  forbidden: "No tenés permiso para hacer esto.",
  mfa_required: "Primero completá la verificación en dos pasos.",
  invalid_email: "Revisá el mail: no parece válido.",
  weak_password: "La contraseña tiene que tener entre 10 y 128 caracteres.",
  invalid_code: "El código no es válido.",
  email_in_use: "Ya hay una cuenta con ese mail. Ingresá con ella y después canjeá el código.",
  already_member: "Ya sos parte de esa escuela. No hace falta canjear este código.",
  invalid_token: "El enlace no es válido o ya venció. Pedí uno nuevo.",
  already_enrolled: "La verificación en dos pasos ya está activada en tu cuenta.",
  not_found: "No encontramos lo que buscabas.",
  member_has_account: "Esa persona ya tiene una cuenta.",
  body_too_large: "El archivo es demasiado grande. El máximo es 1 MB.",
  unsupported_media_type: "El archivo tiene que ser CSV.",
  invalid_rows: "El archivo tiene filas con errores. No se cargó nada.",
  too_many_rows: "El archivo tiene más de 5000 filas. Dividilo en partes.",
  network: "No pudimos conectarnos. Revisá tu conexión e intentá de nuevo.",
};

/** Where the error happened, when the same code needs different advice. */
export type ErrorContext = "access-code" | "mfa" | "reset";

const contextual: Record<ErrorContext, Record<string, string>> = {
  "access-code": {
    invalid_code: "El código no es válido, ya se usó o venció. Pedile uno nuevo a tu escuela.",
  },
  mfa: {
    invalid_code: "El código no es correcto o ya se usó. Probá con el próximo que muestre tu app.",
  },
  reset: {
    invalid_token: "El enlace para restablecer no es válido o ya venció. Pedí uno nuevo.",
  },
};

export function errorMessage(code: string, context?: ErrorContext): string {
  return (context && contextual[context][code]) ?? messages[code] ?? GENERIC;
}
