import type { LucideIcon } from 'lucide-react'
import {
  BellIcon,
  Building2,
  CalendarDays,
  DoorOpen,
  FolderOpenIcon,
  Gavel,
  HomeIcon,
  KeyRound,
  LayoutDashboard,
  LifeBuoy,
  MegaphoneIcon,
  SettingsIcon,
  UserRound,
  Users,
  UsersRound,
  Wallet,
} from 'lucide-react'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import type { Me } from '@/api/schema'
import { moneyAccess } from '@/lib/money-access'
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
    id: 'portfolio',
    label: msg`Portfolio`,
    to: '/orgs/$orgId/portfolio',
    icon: LayoutDashboard,
    permission: 'dashboard.portfolio',
  },
  {
    id: 'sites',
    label: msg`Sites`,
    to: '/orgs/$orgId/sites',
    icon: Building2,
    permission: 'sites.read',
  },
  {
    id: 'units',
    label: msg`Units`,
    to: '/orgs/$orgId/units',
    icon: DoorOpen,
    permission: 'units.read',
  },
  {
    id: 'people',
    label: msg`People`,
    to: '/orgs/$orgId/people',
    icon: Users,
    permission: 'people.read',
  },
  {
    id: 'money',
    label: msg`Money`,
    to: '/orgs/$orgId/money',
    icon: Wallet,
    visible: ({ me, orgId }) => moneyAccess(me, orgId).any,
  },
  {
    id: 'tickets',
    label: msg`Tickets`,
    to: '/orgs/$orgId/tickets',
    icon: LifeBuoy,
    visible: ({ me, orgId }) =>
      ['tickets.read', 'tickets.read_assigned', 'tickets.work', 'tickets.open'].some((p) =>
        can(me, orgId, p),
      ),
  },
  {
    id: 'announcements',
    label: msg`Announcements`,
    to: '/orgs/$orgId/announcements',
    icon: MegaphoneIcon,
    visible: ({ me, orgId }) =>
      can(me, orgId, 'announcements.read') || can(me, orgId, 'announcements.publish'),
  },
  {
    id: 'documents',
    label: msg`Documents`,
    to: '/orgs/$orgId/documents',
    icon: FolderOpenIcon,
    permission: 'documents.read',
  },
  {
    id: 'notifications',
    label: msg`Notifications`,
    to: '/orgs/$orgId/notifications',
    icon: BellIcon,
    permission: 'notifications.read',
  },
  {
    id: 'settings',
    label: msg`Settings`,
    to: '/orgs/$orgId/settings',
    icon: SettingsIcon,
    visible: ({ me, orgId }) =>
      [
        'org.read',
        'org.manage',
        'audit.read',
        'tickets.manage',
        'notifications.manage',
        'packs.read',
        'apikeys.read',
        'webhooks.read',
      ].some((p) => can(me, orgId, p)),
  },
  {
    id: 'meetings',
    label: msg`Meetings`,
    to: '/orgs/$orgId/meetings',
    icon: Gavel,
    visible: ({ me, orgId }) =>
      can(me, orgId, 'meetings.read') ||
      can(me, orgId, 'meetings.manage') ||
      can(me, orgId, 'polls.answer'),
  },
  {
    id: 'bookings',
    label: msg`Bookings`,
    to: '/orgs/$orgId/bookings',
    icon: CalendarDays,
    visible: ({ me, orgId }) =>
      can(me, orgId, 'bookings.read') ||
      can(me, orgId, 'bookings.create') ||
      can(me, orgId, 'bookings.manage'),
  },
  {
    id: 'visitors',
    label: msg`Visitors`,
    to: '/orgs/$orgId/visitors',
    icon: UsersRound,
    visible: ({ me, orgId }) =>
      can(me, orgId, 'visitors.read') ||
      can(me, orgId, 'visitors.manage') ||
      can(me, orgId, 'visitors.decide'),
  },
  {
    id: 'access',
    label: msg`Access`,
    to: '/orgs/$orgId/access',
    icon: KeyRound,
    permission: 'access.read',
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
