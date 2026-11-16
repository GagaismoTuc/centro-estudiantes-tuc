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
  invalid_amount: "Ingres\u00e1 un monto mayor a cero.",
  invalid_date: "Ingres\u00e1 una fecha v\u00e1lida.",
  future_date: "La fecha no puede ser futura.",
  description_required: "Escrib\u00ed una descripci\u00f3n para el precio de referencia.",
  description_too_long: "La descripci\u00f3n puede tener hasta 500 caracteres.",
  invalid_category: "Eleg\u00ed una categor\u00eda v\u00e1lida para el movimiento.",
  reserved_category: "Esa categor\u00eda est\u00e1 reservada para pagos de cuotas.",
  already_reversed: "Ese movimiento ya fue revertido. Actualizá la caja para ver la reversión.",
  cannot_reverse_reversal: "Una reversión no se puede revertir. Para corregirla, cargá un movimiento nuevo.",
  member_has_account: "Esa persona ya tiene una cuenta.",
  body_too_large: "El archivo es demasiado grande. El máximo es 1 MB.",
  unsupported_media_type: "El archivo tiene que ser CSV.",
  invalid_rows: "El archivo tiene filas con errores. No se cargó nada.",
  too_many_rows: "El archivo tiene más de 5000 filas. Dividilo en partes.",
  name_required: "Escribí un nombre.",
  category_exists: "Ya existe una categor\u00eda con ese nombre y sentido.",
  category_in_use: "La categor\u00eda tiene movimientos. Archivala en lugar de borrarla.",
  heading_exists: "Ya existe un rubro con ese nombre.",
  heading_in_use: "Ese rubro tiene contactos asociados. Archivalo en lugar de borrarlo.",
  reserved_heading: "El rubro «otros» es parte del sistema y no se puede editar, archivar ni borrar.",
  invalid_heading: "Elegí un rubro activo válido.",
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
  election_annulled: "La elección fue anulada: ya no se puede cambiar.",
  election_published: "La votación ya terminó y el resultado está publicado: no se puede anular.",
  reason_required: "Escribí el motivo antes de anular la elección.",
  reason_too_long: "El motivo puede tener hasta 500 caracteres.",
  network: "No pudimos conectarnos. Revisá tu conexión e intentá de nuevo.",
};

/** Where the error happened, when the same code needs different advice. */
export type ErrorContext = "finance" | "access-code" | "change-password" | "mfa" | "mfa-recovery" | "reset" | "verify";

const contextual: Record<ErrorContext, Record<string, string>> = {
  finance: {
    invalid_amount: "Ingres\u00e1 un monto positivo en centavos enteros.",
    invalid_date: "Eleg\u00ed una fecha v\u00e1lida.",
    description_required: "Escrib\u00ed una descripci\u00f3n.",
  },
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
  invalid_amount: ["amount"],
  invalid_date: ["date"],
  future_date: ["date"],
  description_required: ["description"],
  description_too_long: ["description"],
  invalid_credentials: ["email", "password", "currentPassword"],
  invalid_email: ["email"],
  email_in_use: ["email"],
  weak_password: ["password", "newPassword"],
  same_password: ["newPassword"],
  invalid_code: ["code", "recoveryCode"],
  name_required: ["name"],
  category_exists: ["name"],
  heading_exists: ["name"],
  invalid_heading: ["headingId"],
  invalid_name: ["name"],
  invalid_window: ["startsAt", "endsAt"],
  invalid_submission_period: ["submissionStartsAt", "submissionEndsAt"],
  invalid_positions: ["position"],
  mode_not_supported: ["mode"],
};

export function errorFields(code: string): string[] {
  return fieldsByCode[code] ?? [];
}
