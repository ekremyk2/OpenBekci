/**
 * Site-scoped dates are never formatted in the browser's timezone (web plan §3).
 *  - A calendar date ("2026-03-01", no time) has no timezone: it is rendered as written.
 *  - An instant (ISO timestamp with offset/Z, or Date) is rendered in the SITE's timezone.
 */

export type DateKind = 'date' | 'datetime' | 'time'

const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/

/** Today's calendar date (`YYYY-MM-DD`) in the given time zone, not the browser's or UTC's. */
export function todayInZone(timeZone: string, now: Date = new Date()): string {
  let zone = timeZone
  try {
    new Intl.DateTimeFormat('en', { timeZone: zone })
  } catch {
    zone = 'UTC'
  }
  // the en-CA locale writes dates as YYYY-MM-DD
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: zone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(now)
}

/** Time zone of the site with this id (falls back to `fallback`). */
export function zoneOfSite(
  sites: readonly { id: string; timezone: string }[] | undefined,
  siteId: string | null | undefined,
  fallback = 'UTC',
): string {
  return sites?.find((s) => s.id === siteId)?.timezone ?? fallback
}

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

/**
 * The instant (RFC 3339, with the site's UTC offset) at which `isoDate` starts in `timeZone`:
 * `2026-03-05` in Europe/Istanbul is `2026-03-05T00:00:00+03:00`. Used where the contract still
 * asks for a `date-time` although the business fact is a calendar day (M4 backdating works by the
 * site's calendar day, so the browser's own timezone must never leak in).
 */
export function siteMidnight(isoDate: string, timeZone: string): string {
  const [y, m, d] = isoDate.split('-').map(Number) as [number, number, number]
  const wall = Date.UTC(y, m - 1, d, 0, 0, 0)
  const offsetAt = (instant: number): number => {
    const part = new Intl.DateTimeFormat('en-US', {
      timeZone,
      timeZoneName: 'longOffset',
      hour: '2-digit',
    })
      .formatToParts(new Date(instant))
      .find((p) => p.type === 'timeZoneName')?.value
    const match = /GMT([+-])(\d{2}):?(\d{2})?/.exec(part ?? '')
    if (!match) return 0
    const minutes = Number(match[2]) * 60 + Number(match[3] ?? 0)
    return (match[1] === '-' ? -1 : 1) * minutes
  }
  // the offset in force at local midnight, found by settling on a fixed point (DST edges)
  let offset = offsetAt(wall)
  offset = offsetAt(wall - offset * 60_000)
  const abs = Math.abs(offset)
  const sign = offset < 0 ? '-' : '+'
  const hh = String(Math.floor(abs / 60)).padStart(2, '0')
  const mm = String(abs % 60).padStart(2, '0')
  return `${isoDate}T00:00:00${sign}${hh}:${mm}`
}
