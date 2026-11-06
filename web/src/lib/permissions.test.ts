import { describe, expect, it } from 'vitest'
import { can } from './permissions'
import { meFixture } from '@/test/handlers'

describe('can()', () => {
  it('grants organization-wide permissions', () => {
    expect(can(meFixture, 'org-1', 'site.read')).toBe(true)
    expect(can(meFixture, 'org-1', 'site.read', { siteId: 'anything' })).toBe(true)
  })

  it('denies permissions the user does not hold', () => {
    expect(can(meFixture, 'org-1', 'ledger.post')).toBe(false)
  })

  it('applies site scope when a scope is asked about', () => {
    expect(can(meFixture, 'org-1', 'ledger.read', { siteId: 'site-a' })).toBe(true)
    expect(can(meFixture, 'org-1', 'ledger.read', { siteId: 'site-b' })).toBe(false)
  })

  it('treats a scoped grant as "somewhere" when no scope is given', () => {
    expect(can(meFixture, 'org-1', 'ledger.read')).toBe(true)
  })

  it('denies for unknown organizations and missing sessions', () => {
    expect(can(meFixture, 'nope', 'site.read')).toBe(false)
    expect(can(undefined, 'org-1', 'site.read')).toBe(false)
  })

  it('treats * as an ordinary string, never as a wildcard', () => {
    const me = {
      ...meFixture,
      organizations: [{ ...meFixture.organizations[0]!, permissions: [{ permission: '*' }] }],
    }
    expect(can(me, 'org-1', 'anything.at_all')).toBe(false)
  })
})
