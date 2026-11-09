import { http, HttpResponse } from 'msw'
import type { Me } from '@/api/schema'
import { discoveryFixture, meFixture } from '../handlers'
import { db } from './db'

/** Every permission the setup and people screens look at, granted organization-wide. */
export const ALL_PERMISSIONS = [
  'org.read',
  'org.manage',
  'sites.read',
  'sites.manage',
  'sites.bank_details',
  'sites.export',
  'sites.offboard',
  'units.read',
  'units.manage',
  'units.import',
  'vehicles.read',
  'vehicles.manage',
  'jobs.read',
  'people.read',
  'people.manage',
  'people.national_id',
  'people.export',
  'people.erase',
  'people.move_out',
  'occupancies.read',
  'occupancies.manage',
  'occupancies.grant_money',
  'memberships.read',
  'memberships.manage',
  'invitations.manage',
  'claims.decide',
  'directory.read',
  'vendors.read',
  'vendors.manage',
  'roles.read',
  'money.read',
  'money.post',
  'money.lock',
  'dashboard.portfolio',
  // operations
  'tickets.read',
  'tickets.manage',
  'tickets.open',
  'announcements.read',
  'announcements.publish',
  'documents.read',
  'documents.manage',
  'files.upload',
  'files.read',
  'notifications.read',
  'notifications.manage',
  'audit.read',
  'dashboard.site',
  'codes.read',
  // money
  'money.manage',
  'payments.record',
  'collections.manage',
  'statements.read',
  'reports.read',
  'reports.debtors',
  'reports.aggregate',
  // governance, community and access
  'meetings.read',
  'meetings.manage',
  'decisions.manage',
  'attendance.manage',
  'facilities.manage',
  'bookings.read',
  'bookings.create',
  'bookings.manage',
  'visitors.read',
  'visitors.manage',
  'visitors.decide',
  'gate.operate',
  'gate.manage',
  'access.read',
  'access.manage',
  'access.open',
  'access.view_events',
  // integrations (0.4)
  'packs.read',
  'packs.manage',
  'chat.manage',
  'apikeys.read',
  'apikeys.manage',
  'webhooks.read',
  'webhooks.manage',
  // card checkout (0.4)
  'payments.checkout',
]

/** A signed-in organization admin with everything granted, for mock mode and screen tests. */
export function adminMe(
  permissions: string[] = ALL_PERMISSIONS,
  occupancies: Me['occupancies'] = meFixture.occupancies,
): Me {
  return {
    ...meFixture,
    occupancies,
    user: { ...meFixture.user, locale: 'en' },
    organizations: [
      {
        ...meFixture.organizations[0]!,
        roles: [{ role: 'org_admin', scope_type: 'org' }],
        permissions: permissions.map((permission) => ({ permission })),
        // the site list follows the in-memory backend, so a site created in a test shows up
        sites: db.sites.map((x) => ({
          id: x.id,
          name: x.name,
          timezone: x.timezone,
          currency: x.currency,
        })),
      },
    ],
  }
}

export const adminHandlers = (permissions?: string[], occupancies?: Me['occupancies']) => [
  http.get('*/v1/me', () => HttpResponse.json(adminMe(permissions, occupancies))),
  http.get('*/.well-known/openbekci.json', () => HttpResponse.json(discoveryFixture)),
]

/** What a signed-in resident of unit A1 may do: own statement, declare payments, take part. */
const RESIDENT_PERMISSIONS = [
  'statements.read_own',
  'payments.declare',
  'payments.checkout',
  'meetings.read',
  'polls.answer',
  'bookings.read',
  'bookings.create',
  'visitors.manage',
  'visitors.decide',
  'deliveries.read',
]
/** The guard of the booth: the gate console only. */
const GUARD_PERMISSIONS = ['gate.operate', 'access.open']

/** A resident (`?mockRole=resident`) or a guard (`?mockRole=guard`), for the portal and gate views. */
export function roleMe(role: 'resident' | 'guard'): Me {
  const org = adminMe().organizations[0]!
  const permissions = role === 'resident' ? RESIDENT_PERMISSIONS : GUARD_PERMISSIONS
  return {
    ...meFixture,
    organizations: [
      {
        ...org,
        roles: [],
        permissions: permissions.map((permission) => ({ permission })),
      },
    ],
    // the guard of the booth lives nowhere: no unit, so no Money
    occupancies:
      role === 'guard'
        ? []
        : [
            {
              id: 'occ-mock',
              organization_id: org.id,
              site_id: 'site-a',
              unit_id: 'u-a1',
              unit_label: 'A1',
              kind: 'owner',
              may_see_money: true,
            },
          ],
  }
}
