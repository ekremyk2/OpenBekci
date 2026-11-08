import type { S } from '@/features/meetings/kit/api'
import { hhmmToMinutes } from '@/features/meetings/kit/time'

/**
 * Facility booking rules (C1), evaluated in the SITE's timezone. The server decides; this mirrors
 * it so the form can explain a refusal before the request (and show the fee).
 */
export type Rules = S['FacilityRules']
export type BookingIssue =
  | 'end_before_start'
  | 'too_long'
  | 'too_soon'
  | 'too_far_ahead'
  | 'outside_hours'
  | 'quota_exceeded'
  | 'not_allowed'

/** ISO weekday (1 = Monday .. 7 = Sunday) and minutes since midnight of an instant in `timeZone`. */
export function zonedWeekdayMinutes(iso: string | Date, timeZone: string) {
  const d = typeof iso === 'string' ? new Date(iso) : iso
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    weekday: 'short',
    hourCycle: 'h23',
    hour: '2-digit',
    minute: '2-digit',
  }).formatToParts(d)
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? ''
  const map: Record<string, number> = { Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6, Sun: 7 }
  return {
    weekday: map[get('weekday')] ?? 1,
    minutes: Number(get('hour')) * 60 + Number(get('minute')),
  }
}

export interface BookingCheck {
  rules: Rules
  start: string
  end: string
  timeZone: string
  now?: Date
  /** confirmed bookings of this unit on this facility in the current quota period */
  usedInPeriod?: number
  /** the booker's side of the unit: owner / tenant / household */
  bookerKind?: 'owner' | 'tenant' | 'household'
}

/** The rules a booking would break (empty when it is fine). */
export function validateBooking(c: BookingCheck): BookingIssue[] {
  const issues: BookingIssue[] = []
  const { rules } = c
  const now = c.now ?? new Date()
  const s = Date.parse(c.start)
  const e = Date.parse(c.end)
  if (Number.isNaN(s) || Number.isNaN(e) || e <= s) return ['end_before_start']
  const minutes = (e - s) / 60_000
  if (minutes > rules.max_duration_minutes) issues.push('too_long')
  if (rules.min_notice_minutes !== null && s - now.getTime() < rules.min_notice_minutes * 60_000)
    issues.push('too_soon')
  if (s - now.getTime() > rules.max_days_ahead * 86_400_000) issues.push('too_far_ahead')
  const a = zonedWeekdayMinutes(c.start, c.timeZone)
  const b = zonedWeekdayMinutes(c.end, c.timeZone)
  const sameDay = b.weekday === a.weekday && b.minutes >= a.minutes
  const open = rules.opening_hours.some(
    (h) =>
      h.days.includes(a.weekday) &&
      sameDay &&
      a.minutes >= hhmmToMinutes(h.start) &&
      b.minutes <= hhmmToMinutes(h.end),
  )
  if (!open) issues.push('outside_hours')
  if (rules.quota_per_unit !== null && (c.usedInPeriod ?? 0) >= rules.quota_per_unit)
    issues.push('quota_exceeded')
  if (c.bookerKind && !rules.who_may_book.includes(c.bookerKind)) issues.push('not_allowed')
  return issues
}

/**
 * Refund of the fee on cancellation: refunded in full when cancelled at least
 * `cancel_refund_cutoff_hours` before the start, otherwise kept.
 */
export function refundOnCancel(rules: Rules, start: string, now = new Date()) {
  if (!rules.fee) return { fee: null, refunded: false }
  const cutoff = (rules.cancel_refund_cutoff_hours ?? 0) * 3_600_000
  return { fee: rules.fee, refunded: Date.parse(start) - now.getTime() >= cutoff }
}

/** Short human summary of opening hours, e.g. "Mon-Fri 10:00-20:00". */
export function daysLabel(days: number[], names: string[]): string {
  const sorted = [...new Set(days)].sort((x, y) => x - y)
  if (sorted.length === 7) return `${names[0]}-${names[6]}`
  const runs: number[][] = []
  for (const d of sorted) {
    const last = runs[runs.length - 1]
    if (last && d === last[last.length - 1]! + 1) last.push(d)
    else runs.push([d])
  }
  return runs
    .map((r) =>
      r.length > 2
        ? `${names[r[0]! - 1]}-${names[r[r.length - 1]! - 1]}`
        : r.map((d) => names[d - 1]).join(', '),
    )
    .join(', ')
}
