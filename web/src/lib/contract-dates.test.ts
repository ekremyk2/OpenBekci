import { describe, expect, it } from 'vitest'
import { toContractDate } from './contract-dates'
import { siteMidnight } from './dates'

const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/

describe('siteMidnight', () => {
  it('is local midnight with the site offset, not the browser timezone', () => {
    expect(siteMidnight('2026-03-05', 'Europe/Istanbul')).toBe('2026-03-05T00:00:00+03:00')
    expect(siteMidnight('2026-07-01', 'Europe/Berlin')).toBe('2026-07-01T00:00:00+02:00')
    expect(siteMidnight('2026-01-01', 'Europe/Berlin')).toBe('2026-01-01T00:00:00+01:00')
    expect(siteMidnight('2026-01-01', 'America/New_York')).toBe('2026-01-01T00:00:00-05:00')
    expect(siteMidnight('2026-01-01', 'UTC')).toBe('2026-01-01T00:00:00+00:00')
  })

  it('uses the offset in force at midnight on a daylight-saving change day', () => {
    // Berlin springs forward at 02:00 on 2026-03-29: midnight is still +01:00
    expect(siteMidnight('2026-03-29', 'Europe/Berlin')).toBe('2026-03-29T00:00:00+01:00')
    expect(siteMidnight('2026-03-30', 'Europe/Berlin')).toBe('2026-03-30T00:00:00+02:00')
  })

  it('names the same calendar day in the site timezone', () => {
    const instant = new Date(siteMidnight('2026-03-05', 'Europe/Istanbul'))
    const day = new Intl.DateTimeFormat('en-CA', { timeZone: 'Europe/Istanbul' }).format(instant)
    expect(day).toBe('2026-03-05')
  })
})

describe('toContractDate', () => {
  it('sends an RFC 3339 timestamp under the contract field name', () => {
    const r = toContractDate('received', '2026-03-05', 'Europe/Istanbul')
    expect(r).toEqual({ received_at: '2026-03-05T00:00:00+03:00' })
    expect(r.received_at).toMatch(RFC3339)
    expect(toContractDate('transferred', '2026-03-05', 'UTC')).toEqual({
      transferred_at: '2026-03-05T00:00:00+00:00',
    })
    expect(toContractDate('refunded', '2026-03-05', 'UTC')).toEqual({
      refunded_at: '2026-03-05T00:00:00+00:00',
    })
  })
})
