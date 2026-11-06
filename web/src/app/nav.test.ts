import { describe, expect, it } from 'vitest'
import type { Me } from '@/api/schema'
import { meFixture } from '@/test/handlers'
import { filterNav, NAV_ITEMS, type NavItem } from './nav'

const ids = (items: NavItem[]) => items.map((i) => i.id)
const ctx = { me: meFixture, orgId: 'org-1' }

describe('filterNav', () => {
  it('hides planned items until their route exists', () => {
    expect(ids(filterNav(NAV_ITEMS, ctx))).toEqual(['home', 'me'])
  })

  it('filters by permission when planned items are included', () => {
    const all = ids(filterNav(NAV_ITEMS, ctx, { includePlanned: true }))
    expect(all).toContain('sites') // site.read granted
    expect(all).not.toContain('people') // person.read missing
    expect(all).not.toContain('tickets')
    expect(all).toContain('money') // ledger.read granted (scoped)
  })

  it('shows money to occupants who may see money, even without ledger rights', () => {
    const resident: Me = {
      ...meFixture,
      organizations: [{ ...meFixture.organizations[0]!, permissions: [], roles: [] }],
    }
    expect(
      ids(filterNav(NAV_ITEMS, { me: resident, orgId: 'org-1' }, { includePlanned: true })),
    ).toContain('money')
    const household: Me = {
      ...resident,
      occupancies: resident.occupancies.map((o) => ({ ...o, may_see_money: false })),
    }
    expect(
      ids(filterNav(NAV_ITEMS, { me: household, orgId: 'org-1' }, { includePlanned: true })),
    ).not.toContain('money')
  })

  it('shows only unconditional items for an unknown organization', () => {
    expect(ids(filterNav(NAV_ITEMS, { me: meFixture, orgId: 'other' }))).toEqual(['home', 'me'])
  })
})
