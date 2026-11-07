import type { LucideIcon } from 'lucide-react'
import { Building2, HomeIcon, LifeBuoy, UserRound, Users, Wallet } from 'lucide-react'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import type { Me } from '@/api/schema'
import { can } from '@/lib/permissions'

/**
 * Navigation manifest (web plan §2/§8). Each item declares what it needs; the sidebar shows only
 * what survives `filterNav`. `planned` items document upcoming areas and are never rendered until
 * their route exists (flip the flag when the route lands).
 * Permission names below other than the ones in the plan are PROVISIONAL until the spec lands.
 */
export interface NavItem {
  id: string
  label: MessageDescriptor
  /** Path template, `$orgId` is substituted. */
  to: string
  icon: LucideIcon
  /** Effective permission required in the current organization. */
  permission?: string
  /** Extra predicate for portal rules driven by occupancies. */
  visible?: (ctx: NavContext) => boolean
  planned?: boolean
}

export interface NavContext {
  me: Me
  orgId: string
}

export const NAV_ITEMS: NavItem[] = [
  { id: 'home', label: msg`Home`, to: '/orgs/$orgId', icon: HomeIcon },
  {
    id: 'sites',
    label: msg`Sites`,
    to: '/orgs/$orgId/sites',
    icon: Building2,
    permission: 'site.read',
    planned: true,
  },
  {
    id: 'people',
    label: msg`People`,
    to: '/orgs/$orgId/people',
    icon: Users,
    permission: 'person.read',
    planned: true,
  },
  {
    id: 'money',
    label: msg`Money`,
    to: '/orgs/$orgId/money',
    icon: Wallet,
    visible: ({ me, orgId }) =>
      can(me, orgId, 'money.read') ||
      can(me, orgId, 'statements.read_own') ||
      me.occupancies.some((o) => o.organization_id === orgId && o.may_see_money),
  },
  {
    id: 'tickets',
    label: msg`Tickets`,
    to: '/orgs/$orgId/tickets',
    icon: LifeBuoy,
    permission: 'ticket.read',
    planned: true,
  },
  { id: 'me', label: msg`My account`, to: '/orgs/$orgId/me', icon: UserRound },
]

export function filterNav(
  items: readonly NavItem[],
  ctx: NavContext,
  options: { includePlanned?: boolean } = {},
): NavItem[] {
  return items.filter((item) => {
    if (item.planned && !options.includePlanned) return false
    if (item.permission && !can(ctx.me, ctx.orgId, item.permission)) return false
    if (item.visible && !item.visible(ctx)) return false
    return true
  })
}
