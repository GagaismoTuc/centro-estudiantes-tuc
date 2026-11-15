// Spanish messages for every `error` code in openapi/openapi.yaml. The UI never shows raw codes.

const GENERIC = "Algo salió mal de nuestro lado. Probá de nuevo en un rato.";

const messages: Record<string, string> = {
  invalid_credentials: "El mail o la contraseña no coinciden.",
  too_many_attempts: "Hiciste demasiados intentos. Esperá unos 15 minutos y probá de nuevo.",
  bad_request: "No pudimos procesar el pedido. Revisá los datos e intentá otra vez.",
  unauthenticated: "Tu sesión terminó. Ingresá de nuevo.",
  forbidden: "No tenés permiso para hacer esto.",
  mfa_required: "Primero completá la verificación en dos pasos.",
  invalid_email: "Revisá el mail: no parece válido.",
  weak_password: "La contraseña tiene que tener entre 10 y 128 caracteres.",
  same_password: "La contraseña nueva tiene que ser distinta de la actual.",
  invalid_code: "El código no es válido.",
  email_in_use: "Ya hay una cuenta con ese mail. Ingresá con ella y después canjeá el código.",
  already_member: "Ya sos parte de esa escuela. No hace falta canjear este código.",
  invalid_token: "El enlace no es válido o ya venció. Pedí uno nuevo.",
  already_enrolled: "La verificación en dos pasos ya está activada en tu cuenta.",
  not_found: "No encontramos lo que buscabas.",
  already_officer: "Ese alumno ya tiene un cargo.",
  position_taken: "Ese cargo ya est\u00e1 ocupado.",
  invalid_position: "Eleg\u00ed un cargo v\u00e1lido.",
  member_has_account: "Esa persona ya tiene una cuenta.",
  body_too_large: "El archivo es demasiado grande. El máximo es 1 MB.",
  unsupported_media_type: "El archivo tiene que ser CSV.",
  invalid_rows: "El archivo tiene filas con errores. No se cargó nada.",
  too_many_rows: "El archivo tiene más de 5000 filas. Dividilo en partes.",
  election_frozen: "La elección ya abrió: no se puede cambiar el Centro de estudiantes.",
  empty_commission: "Elegí al menos un integrante para el Centro de estudiantes.",
  no_account: "Ese alumno todavía no tiene cuenta. Pedile que la active antes de sumarlo al Centro de estudiantes.",
  election_in_progress: "Ya hay una elección en curso en la escuela. Terminala antes de crear otra.",
  election_not_draft: "La elección ya no es un borrador: no se puede editar.",
  invalid_name: "Escribí un nombre para la elección de hasta 120 caracteres.",
  invalid_window: "Revisá las fechas de votación: el inicio tiene que ser futuro y el fin, posterior al inicio.",
  invalid_submission_period: "Revisá la presentación de listas: tiene que terminar antes de que empiece la votación.",
  invalid_positions: "Revisá los cargos: tiene que haber al menos uno, sin nombres vacíos ni repetidos.",
  mode_not_supported: "Por ahora solo se puede votar con voto secreto.",
  network: "No pudimos conectarnos. Revisá tu conexión e intentá de nuevo.",
};

/** Where the error happened, when the same code needs different advice. */
export type ErrorContext = "access-code" | "change-password" | "mfa" | "mfa-recovery" | "reset" | "verify";

const contextual: Record<ErrorContext, Record<string, string>> = {
  "access-code": {
    invalid_code: "El código no es válido, ya se usó o venció. Pedile uno nuevo a tu escuela.",
  },
  "change-password": {
    invalid_credentials: "La contraseña actual no es correcta.",
  },
  mfa: {
    invalid_code: "El código no es correcto o ya se usó. Probá con el próximo que muestre tu app.",
  },
  "mfa-recovery": {
    invalid_code: "El código de recuperación no es correcto o ya se usó. Revisá cómo lo escribiste o probá con otro de los que guardaste.",
  },
  reset: {
    invalid_token: "El enlace para restablecer no es válido o ya venció. Pedí uno nuevo.",
  },
  // The API answers used, expired and unknown links alike, so one sentence covers them.
  verify: {
    invalid_token: "El enlace ya se usó o venció.",
  },
};

export function errorMessage(code: string, context?: ErrorContext): string {
  return (context && contextual[context][code]) ?? messages[code] ?? GENERIC;
}

/** Form fields an error code is about, so the form can mark them invalid and focus the first. */
const fieldsByCode: Record<string, string[]> = {
  invalid_credentials: ["email", "password", "currentPassword"],
  invalid_email: ["email"],
  email_in_use: ["email"],
  weak_password: ["password", "newPassword"],
  same_password: ["newPassword"],
  invalid_code: ["code", "recoveryCode"],
  invalid_name: ["name"],
  invalid_window: ["startsAt", "endsAt"],
  invalid_submission_period: ["submissionStartsAt", "submissionEndsAt"],
  invalid_positions: ["position"],
  mode_not_supported: ["mode"],
};

export function errorFields(code: string): string[] {
  return fieldsByCode[code] ?? [];
}
