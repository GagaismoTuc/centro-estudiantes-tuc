// Every date and time the web shows or sends goes through this module: Argentina time
// on a 24-hour clock, dates as dd/mm/aaaa. Argentina has no daylight saving time, so
// every value is at -03:00.
const ZONE = "America/Argentina/Buenos_Aires";
const OFFSET_MS = 3 * 60 * 60 * 1000;

const numericDateTime = new Intl.DateTimeFormat("es-AR", {
  timeZone: ZONE,
  day: "2-digit",
  month: "2-digit",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
});
const longDateTime = new Intl.DateTimeFormat("es-AR", {
  timeZone: ZONE,
  day: "numeric",
  month: "long",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
});
const longDate = new Intl.DateTimeFormat("es-AR", { dateStyle: "long", timeZone: ZONE });
const isoDate = new Intl.DateTimeFormat("en-CA", { timeZone: ZONE, year: "numeric", month: "2-digit", day: "2-digit" });

/** An RFC 3339 instant as `dd/mm/aaaa HH:mm` in Argentina time ("20/11/2026 22:45"). */
export function formatDateTime(iso: string): string {
  const parts: Record<string, string> = {};
  for (const part of numericDateTime.formatToParts(new Date(iso))) parts[part.type] = part.value;
  return `${parts.day}/${parts.month}/${parts.year} ${parts.hour}:${parts.minute}`;
}

/** An ISO `YYYY-MM-DD` date as `dd/mm/aaaa` ("20/11/2026"). */
export function formatDate(isoDate: string): string {
  return isoDate.split("-").reverse().join("/");
}

/** "10 de marzo de 2027 a las 08:00", in Argentina time. */
export function formatLongDateTime(iso: string): string {
  return longDateTime.format(new Date(iso));
}

/** "10 de marzo de 2027", in Argentina time. */
export function formatLongDate(iso: string): string {
  return longDate.format(new Date(iso));
}

/** Today's `YYYY-MM-DD` date in Argentina time. */
export function todayArgentina(): string {
  return isoDate.format(new Date());
}

/** RFC 3339 → the value of a datetime-local input, in Argentina time ("2027-03-10T08:00"). */
export function toLocalInput(iso: string): string {
  return new Date(Date.parse(iso) - OFFSET_MS).toISOString().slice(0, 16);
}

/** A datetime-local value read as Argentina time → RFC 3339 with -03:00. */
export function fromLocalInput(value: string): string {
  return `${value.length === 16 ? `${value}:00` : value}-03:00`;
}
