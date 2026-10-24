import { NO_VALUE } from "@warehouse/ui-kit";

/** "2026-10-09 08:05:30Z" for an RFC 3339 instant; the raw text when unparseable. */
export function utcLabel(iso: string | undefined): string {
  if (!iso) return NO_VALUE;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const d = new Date(t).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 19)}Z`;
}

/** The station id rule shared by every node id this UI accepts: 1..64
 *  characters, no whitespace, control characters or "/". */
export function idProblem(what: string, id: string): string | null {
  if (id.length === 0) return `Enter a ${what} id.`;
  if (id.length > 64) return `A ${what} id is at most 64 characters.`;
  // eslint-disable-next-line no-control-regex
  if (/[\s/\u0000-\u001f\u007f]/.test(id)) return `A ${what} id cannot contain spaces, control characters or '/'.`;
  return null;
}

/** A service date must be exactly YYYY-MM-DD and a real calendar date. */
export function serviceDateProblem(raw: string): string | null {
  if (raw === "") return "Enter a service date.";
  if (!/^\d{4}-\d{2}-\d{2}$/.test(raw)) return "A service date is YYYY-MM-DD.";
  const [y, m, d] = raw.split("-").map(Number);
  const date = new Date(Date.UTC(y, m - 1, d));
  if (date.getUTCFullYear() !== y || date.getUTCMonth() !== m - 1 || date.getUTCDate() !== d) {
    return "That is not a real calendar date.";
  }
  return null;
}

/** "12 of 15 stops assigned" for a wave's stop counters. */
export function stopMeter(wave: { assignedStops: number; remainingStops: number }): string {
  return `${wave.assignedStops} of ${wave.assignedStops + wave.remainingStops} stops assigned`;
}
