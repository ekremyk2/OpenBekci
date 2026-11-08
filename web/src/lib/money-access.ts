import type { Me } from '@/api/schema'
import { can } from './permissions'

/**
 * Who sees what in Money (web plan §8), decided in one place for the nav and the section frame.
 *  - manager: `money.read`, the full set of screens.
 *  - collector: `payments.record` without `money.read` (staff such as the kapici collect cash into
 *    their own box): one page, "Record cash".
 *  - resident: owner or tenant of a unit, or a household member the owner allowed to see money
 *    (`may_see_money`). `statements.read_own` alone is not enough for a household member.
 *  - transparency: `reports.aggregate` (an owner reading the site's aggregate reports).
 */
export interface MoneyAccess {
  manager: boolean
  collector: boolean
  resident: boolean
  transparency: boolean
  /** Any reason to show Money at all. */
  any: boolean
}

export function moneyAccess(me: Me | undefined, orgId: string | undefined): MoneyAccess {
  if (!me || !orgId)
    return { manager: false, collector: false, resident: false, transparency: false, any: false }
  const manager = can(me, orgId, 'money.read')
  const collector = !manager && can(me, orgId, 'payments.record')
  const mine = me.occupancies.filter((o) => o.organization_id === orgId)
  const resident =
    !manager &&
    (mine.some((o) => o.may_see_money) ||
      (mine.length === 0 && can(me, orgId, 'statements.read_own')))
  const transparency = can(me, orgId, 'reports.aggregate')
  return {
    manager,
    collector,
    resident,
    transparency,
    any: manager || collector || resident || transparency,
  }
}
