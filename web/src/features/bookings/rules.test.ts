import { describe, expect, it } from 'vitest'
import { refundOnCancel, validateBooking, type Rules } from './rules'

const rules: Rules = {
  opening_hours: [{ days: [1, 2, 3, 4, 5], start: '10:00', end: '20:00' }],
  max_duration_minutes: 120,
  max_days_ahead: 30,
  min_notice_minutes: 60,
  quota_per_unit: 2,
  quota_period: 'month',
  who_may_book: ['owner', 'tenant'],
  fee: { amount: 5000, currency: 'TRY' },
  cancel_refund_cutoff_hours: 24,
}
const now = new Date('2026-03-02T08:00:00Z') // Monday 11:00 in Istanbul
const tz = 'Europe/Istanbul'
// Wednesday 2026-03-04, local time = UTC + 3
const at = (h: number, m = 0) =>
  `2026-03-04T${String(h - 3).padStart(2, '0')}:${String(m).padStart(2, '0')}:00Z`

describe('validateBooking', () => {
  it('accepts a booking inside the rules', () => {
    expect(validateBooking({ rules, start: at(18), end: at(20), timeZone: tz, now })).toEqual([])
  })
  it('judges opening hours in the site timezone, not UTC', () => {
    expect(validateBooking({ rules, start: at(9), end: at(10), timeZone: tz, now })).toContain(
      'outside_hours',
    )
    expect(validateBooking({ rules, start: at(19), end: at(21), timeZone: tz, now })).toContain(
      'outside_hours',
    )
  })
  it('rejects a weekend day', () => {
    const sat = '2026-03-07T12:00:00Z'
    const sat2 = '2026-03-07T13:00:00Z'
    expect(validateBooking({ rules, start: sat, end: sat2, timeZone: tz, now })).toContain(
      'outside_hours',
    )
  })
  it('enforces duration, notice and horizon', () => {
    expect(validateBooking({ rules, start: at(12), end: at(15), timeZone: tz, now })).toContain(
      'too_long',
    )
    expect(
      validateBooking({
        rules,
        start: '2026-03-02T08:30:00Z',
        end: '2026-03-02T09:30:00Z',
        timeZone: tz,
        now,
      }),
    ).toContain('too_soon')
    expect(
      validateBooking({
        rules,
        start: '2026-05-04T15:00:00Z',
        end: '2026-05-04T16:00:00Z',
        timeZone: tz,
        now,
      }),
    ).toContain('too_far_ahead')
  })
  it('enforces the quota and who may book', () => {
    expect(
      validateBooking({ rules, start: at(18), end: at(19), timeZone: tz, now, usedInPeriod: 2 }),
    ).toContain('quota_exceeded')
    expect(
      validateBooking({
        rules,
        start: at(18),
        end: at(19),
        timeZone: tz,
        now,
        bookerKind: 'household',
      }),
    ).toContain('not_allowed')
  })
  it('rejects an end before the start', () => {
    expect(validateBooking({ rules, start: at(19), end: at(18), timeZone: tz, now })).toEqual([
      'end_before_start',
    ])
  })
})

describe('refundOnCancel', () => {
  it('refunds before the cutoff and keeps the fee after it', () => {
    expect(refundOnCancel(rules, at(18), now).refunded).toBe(true)
    expect(refundOnCancel(rules, '2026-03-02T20:00:00Z', now).refunded).toBe(false)
    expect(refundOnCancel({ ...rules, fee: null }, at(18), now).fee).toBeNull()
  })
})
