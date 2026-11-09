import { formatSiteDate } from '@/lib/dates'

/** An instant in the viewer's own time zone (organization-level screens have no site zone). */
export function formatInstant(value: string | null | undefined, locale: string): string {
  if (!value) return ''
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
  return formatSiteDate(value, zone, locale, 'datetime')
}

/** `2026-11-09` from a date input to the end of that day in the viewer's zone. */
export function endOfDayIso(isoDate: string): string {
  const [y, m, d] = isoDate.split('-').map(Number)
  return new Date(y!, m! - 1, d!, 23, 59, 59).toISOString()
}

/** Today as `YYYY-MM-DD` in the viewer's zone, for `min` on date inputs. */
export function todayLocal(now = new Date()): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())}`
}
