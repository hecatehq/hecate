// Canonical UI formatters. Anything that turns a duration, cost,
// timestamp, or integer into a user-facing string goes here so
// callers across features render consistently and we have one
// place to adjust if locale, precision, or wording changes.

/**
 * Format a duration in milliseconds as a compact human string.
 *
 *   - Sub-second: "Nms" (rounded to an integer).
 *   - Under a minute: "N.Ns" below 10s, "Ns" above.
 *   - One minute or more: "Nm Ns".
 *
 * Non-finite or non-positive values render as "0ms" — UI surfaces
 * showing "took: …" prefer a definite zero over an empty cell.
 */
export function formatDurationMs(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0ms";
  if (value < 1000) return `${Math.round(value)}ms`;
  if (value < 60_000) {
    const seconds = value / 1000;
    return `${seconds.toFixed(seconds < 10 ? 1 : 0)}s`;
  }
  // Round once at the second granularity, then split. Computing
  // minutes off the unrounded ms and seconds off the leftover lets a
  // value like 119_999 land at "1m 60s" instead of "2m 0s" — the
  // seconds component rolls past 60 because Math.round operates on
  // the leftover, not on the total.
  const totalSeconds = Math.round(value / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds - minutes * 60;
  return `${minutes}m ${seconds}s`;
}

/**
 * Format the duration between two ISO timestamps. When `end` is
 * omitted, the duration is measured against `Date.now()` — useful
 * for in-progress runs where the end is "now-ish".
 *
 * Returns "" if `start` is missing or unparseable; the caller
 * decides whether to render a placeholder.
 */
export function formatDurationRange(start: string | undefined, end?: string): string {
  if (!start) return "";
  const startMs = Date.parse(start);
  if (!Number.isFinite(startMs)) return "";
  const endMs = end ? Date.parse(end) : Date.now();
  if (!Number.isFinite(endMs)) return "";
  return formatDurationMs(Math.max(0, endMs - startMs));
}

/**
 * Format an amount stored in µUSD (millionths of a US dollar) as a
 * "$N.NNN" string. Hecate persists LLM cost in µUSD for integer
 * math precision; we render to three decimal places so sub-cent
 * amounts surface without exploding to scientific notation.
 *
 * Non-finite or non-positive values render as "$0.000".
 */
export function formatMicrosUSD(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "$0.000";
  return `$${(value / 1_000_000).toFixed(3)}`;
}

/**
 * Format an ISO timestamp with year, short-month, day, hour, minute,
 * second, and short timezone components. Uses the browser's default
 * locale (`Intl.DateTimeFormat(undefined, …)`), so the *order*,
 * *month language*, *separators*, and *AM/PM rendering* still vary
 * across locales — what the explicit options pin is the *set of
 * fields included*, not a stable visual layout. Preferred for any
 * timestamp the user might paste into a bug report because all six
 * fields are always present.
 *
 *   - Empty/missing input → "".
 *   - Unparseable input → the raw `value` (a partial string is more
 *     useful than "Invalid Date" in a tooltip).
 */
export function formatAbsoluteTime(value?: string): string {
  if (!value) return "";
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return value;
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit",
    timeZoneName: "short",
  }).format(new Date(parsed));
}

/**
 * Format an ISO timestamp with the browser's default locale layout
 * for both date and time (the unconfigured `Date.toLocaleString()`).
 * Existing surfaces that already shipped this layout use this so
 * their wording doesn't change; new surfaces should prefer
 * `formatAbsoluteTime`.
 *
 * Empty / unparseable input renders as "".
 */
export function formatLocaleDateTime(value?: string): string {
  if (!value) return "";
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return "";
  return new Date(parsed).toLocaleString();
}

/**
 * Format the time portion of an ISO timestamp using the browser's
 * default locale layout. Returns "—" on missing or unparseable
 * input so table cells stay visually aligned.
 */
export function formatLocaleTime(value?: string): string {
  if (!value) return "—";
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return "—";
  return new Date(parsed).toLocaleTimeString();
}

/**
 * Format an integer with the browser's locale thousand separator.
 * Returns "—" for non-finite values; rounds non-integer inputs
 * before formatting so callers don't need a separate guard.
 */
export function formatInteger(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return Math.round(value).toLocaleString();
}
