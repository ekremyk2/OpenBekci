import { describe, expect, it } from 'vitest'
import { formatSiteDate, todayInZone, zoneOfSite } from './dates'

const norm = (s: string) => s.replace(/[\u00a0\u202f]/g, ' ')

describe('formatSiteDate', () => {
  it('renders an instant in the site timezone, not the runner timezone', () => {
    // 22:30Z on 31 Dec is already 1 Jan 01:30 in Istanbul (UTC+3), still 31 Dec in New York.
    const instant = '2026-12-31T22:30:00Z'
    expect(formatSiteDate(instant, 'Europe/Istanbul', 'en-US')).toBe('Jan 1, 2027')
    expect(formatSiteDate(instant, 'America/New_York', 'en-US')).toBe('Dec 31, 2026')
  })

  it('localises to tr-TR', () => {
    expect(formatSiteDate('2026-03-05T10:00:00Z', 'Europe/Istanbul', 'tr-TR')).toBe('5 Mar 2026')
  })

  it('never shifts a calendar date, whatever the timezone', () => {
    expect(formatSiteDate('2026-03-01', 'Pacific/Kiritimati', 'en-US')).toBe('Mar 1, 2026')
    expect(formatSiteDate('2026-03-01', 'Pacific/Pago_Pago', 'en-US')).toBe('Mar 1, 2026')
  })

  it('formats date and time together in site time', () => {
    expect(
      norm(formatSiteDate('2026-06-01T09:05:00Z', 'Europe/Istanbul', 'en-US', 'datetime')),
    ).toBe('Jun 1, 2026, 12:05 PM')
  })

  it('returns an empty string for invalid input', () => {
    expect(formatSiteDate('not a date', 'UTC', 'en-US')).toBe('')
  })
})

describe('todayInZone', () => {
  it("is the site's calendar date, not the UTC date", () => {
    const now = new Date('2026-12-31T22:30:00Z')
    expect(todayInZone('Europe/Istanbul', now)).toBe('2027-01-01')
    expect(todayInZone('America/New_York', now)).toBe('2026-12-31')
    expect(todayInZone('UTC', now)).toBe('2026-12-31')
    expect(todayInZone('Not/AZone', now)).toBe('2026-12-31')
  })
  it('picks the zone of a site by id', () => {
    const sites = [{ id: 'a', timezone: 'Europe/Istanbul' }]
    expect(zoneOfSite(sites, 'a')).toBe('Europe/Istanbul')
    expect(zoneOfSite(sites, 'zzz', 'UTC')).toBe('UTC')
  })
})
