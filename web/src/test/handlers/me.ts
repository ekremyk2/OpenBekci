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
