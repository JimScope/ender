/**
 * Timezone-aware datetime conversion utilities.
 *
 * The `datetime-local` input returns naive strings like "2026-03-17T14:00"
 * without timezone information. These helpers let us interpret that value
 * in an arbitrary IANA timezone instead of relying on the browser's
 * local timezone.
 */

/** Returns the browser's IANA timezone (e.g. "America/Havana"). */
export function getBrowserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    return "UTC"
  }
}

/**
 * Returns the current instant formatted for a `datetime-local` input's `min`
 * attribute ("YYYY-MM-DDTHH:mm") in the browser's local timezone. The browser
 * uses this string to block selection of past times in the native picker.
 */
export function nowLocalDatetime(): string {
  const d = new Date()
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16)
}

/**
 * Converts a naive datetime string (from a `datetime-local` input) to a
 * UTC ISO-8601 string, interpreting the datetime as being in `timezone`.
 *
 * Example: naiveDatetimeToUTC("2026-03-17T14:00", "America/Havana")
 *   → "2026-03-17T18:00:00.000Z"  (Havana is UTC-4 during CDT)
 */
export function naiveDatetimeToUTC(datetime: string, timezone: string): string {
  // Treat the naive value as if it were UTC so we have a stable reference
  const asUTC = new Date(`${datetime}Z`)

  // Calculate the UTC offset of `timezone` at this approximate instant.
  // By formatting the same instant in both UTC and the target timezone and
  // then parsing both through the *same* browser locale, the browser's own
  // timezone cancels out — the difference is purely UTC-vs-target.
  const offset = getTZOffset(asUTC, timezone)
  const firstGuess = new Date(asUTC.getTime() - offset)

  // Second pass: re-check the offset at the corrected instant to handle
  // datetimes that land right around a DST transition.
  const offset2 = getTZOffset(firstGuess, timezone)
  if (offset !== offset2) {
    return new Date(asUTC.getTime() - offset2).toISOString()
  }

  return firstGuess.toISOString()
}

/**
 * Converts a UTC ISO-8601 string to a naive datetime string
 * ("YYYY-MM-DDTHH:mm") expressed in `timezone`, suitable for a
 * `datetime-local` input.
 */
export function utcToDatetimeInTZ(
  isoString: string | null | undefined,
  timezone: string,
): string {
  if (!isoString) return ""

  const date = new Date(isoString)
  if (Number.isNaN(date.getTime())) return ""

  const fmt = new Intl.DateTimeFormat("sv-SE", {
    timeZone: timezone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  })

  // sv-SE locale produces "YYYY-MM-DD HH:mm" which is close to what
  // datetime-local needs — just swap the space for a "T".
  return fmt.format(date).replace(" ", "T")
}

// ── internal ────────────────────────────────────────────────────────

/** Returns the UTC offset (in ms) of `timezone` at the given instant. */
function getTZOffset(ref: Date, timezone: string): number {
  const utcStr = ref.toLocaleString("en-US", { timeZone: "UTC" })
  const tzStr = ref.toLocaleString("en-US", { timeZone: timezone })
  return new Date(tzStr).getTime() - new Date(utcStr).getTime()
}
