/**
 * Site-scoped dates are never formatted in the browser's timezone (web plan §3).
 *  - A calendar date ("2026-03-01", no time) has no timezone: it is rendered as written.
 *  - An instant (ISO timestamp with offset/Z, or Date) is rendered in the SITE's timezone.
 */

export type DateKind = 'date' | 'datetime' | 'time'

const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/

export function formatSiteDate(
  value: string | Date,
  timeZone: string,
  locale: string,
  kind: DateKind = 'date',
  options: Intl.DateTimeFormatOptions = {},
): string {
  let date: Date
  let tz = timeZone
  if (typeof value === 'string' && DATE_ONLY.test(value)) {
    // Noon UTC rendered in UTC can never roll over to a neighbouring day.
    date = new Date(`${value}T12:00:00Z`)
    tz = 'UTC'
    kind = 'date'
  } else {
    date = typeof value === 'string' ? new Date(value) : value
  }
  if (Number.isNaN(date.getTime())) return ''
  const base: Intl.DateTimeFormatOptions =
    kind === 'date'
      ? { dateStyle: 'medium' }
      : kind === 'time'
        ? { timeStyle: 'short' }
        : { dateStyle: 'medium', timeStyle: 'short' }
  return new Intl.DateTimeFormat(locale, { ...base, timeZone: tz, ...options }).format(date)
}
