/**
 * Site-timezone helpers for `datetime-local` inputs. The browser's timezone is never used for
 * site data (web plan §3): an instant is shown and edited as wall-clock time of the SITE.
 */

function parts(ms: number, timeZone: string) {
  const f = new Intl.DateTimeFormat('en-CA', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
  const p: Record<string, string> = {}
  for (const x of f.formatToParts(new Date(ms))) p[x.type] = x.value
  return p as Record<'year' | 'month' | 'day' | 'hour' | 'minute' | 'second', string>
}

/** `YYYY-MM-DDTHH:mm` wall clock of `iso` in `timeZone` (empty string for an invalid instant). */
export function instantToLocalInput(iso: string, timeZone: string): string {
  const ms = Date.parse(iso)
  if (Number.isNaN(ms)) return ''
  const p = parts(ms, timeZone)
  return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}`
}

/** Instant (UTC ISO string) of a `YYYY-MM-DDTHH:mm` wall clock in `timeZone`. */
export function localInputToInstant(local: string, timeZone: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(local)
  if (!m) return ''
  const wall = Date.UTC(+m[1]!, +m[2]! - 1, +m[3]!, +m[4]!, +m[5]!)
  let guess = wall
  for (let i = 0; i < 3; i++) {
    const p = parts(guess, timeZone)
    const shown = Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour, +p.minute)
    const delta = wall - shown
    if (delta === 0) break
    guess += delta
  }
  return new Date(guess).toISOString()
}

/** `YYYY-MM-DD` of "today" in the site's timezone. */
export function todayIn(timeZone: string, now = new Date()): string {
  const p = parts(now.getTime(), timeZone)
  return `${p.year}-${p.month}-${p.day}`
}

/** Adds whole days to a `YYYY-MM-DD` date. */
export function addDays(isoDate: string, days: number): string {
  const [y, m, d] = isoDate.split('-').map(Number) as [number, number, number]
  return new Date(Date.UTC(y, m - 1, d + days)).toISOString().slice(0, 10)
}

/** Weekday of a `YYYY-MM-DD` date, 1 = Monday .. 7 = Sunday (the contract's day numbering). */
export function isoWeekday(isoDate: string): number {
  const [y, m, d] = isoDate.split('-').map(Number) as [number, number, number]
  const w = new Date(Date.UTC(y, m - 1, d)).getUTCDay()
  return w === 0 ? 7 : w
}

/** Monday of the week containing `isoDate`. */
export function startOfWeek(isoDate: string): string {
  return addDays(isoDate, 1 - isoWeekday(isoDate))
}

export function startOfMonth(isoDate: string): string {
  return `${isoDate.slice(0, 7)}-01`
}

/** Minutes since midnight of an `HH:mm` string. */
export function hhmmToMinutes(v: string): number {
  const [h, m] = v.split(':').map(Number) as [number, number]
  return h * 60 + m
}
