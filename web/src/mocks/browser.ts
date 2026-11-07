import { http, HttpResponse } from 'msw'
import { setupWorker } from 'msw/browser'
import type { Me } from '@/api/schema'
import { discoveryFixture, meFixture } from '@/test/handlers'
import { accessHandlers } from '@/test/handlers/access'
import { communityHandlers } from '@/test/handlers/community'
import { directoryHandlers, governanceHandlers } from '@/test/handlers/governance'
import { OWNERS } from '@/test/handlers/gov-util'

/**
 * `pnpm dev:mock`: the app runs against in-memory handlers, no backend needed. The signed-in user
 * is a site manager with every governance, community and access permission; open the app with
 * `?mockRole=resident` for the resident (portal) view of unit A1, or `?mockRole=guard` for the
 * guard of the booth (gate console only). The guard PIN in the mock is 1234.
 */
const MANAGER_PERMISSIONS = [
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
  'gate.operate',
  'gate.manage',
  'access.read',
  'access.manage',
  'access.open',
  'access.view_events',
]
const RESIDENT_PERMISSIONS = [
  'meetings.read',
  'polls.answer',
  'bookings.read',
  'bookings.create',
  'visitors.manage',
  'visitors.decide',
  'deliveries.read',
]
const GUARD_PERMISSIONS = ['gate.operate', 'access.open']

function buildMe(role: string | null): Me {
  const org = meFixture.organizations[0]!
  const owner = OWNERS[0]!
  const occupancies = [
    {
      id: 'occ-mock',
      organization_id: org.id,
      site_id: 'site-a',
      unit_id: owner.unitId,
      unit_label: owner.door,
      kind: 'owner' as const,
      may_see_money: true,
    },
  ]
  const permissions =
    role === 'resident' ? RESIDENT_PERMISSIONS : role === 'guard' ? GUARD_PERMISSIONS : MANAGER_PERMISSIONS
  return {
    ...meFixture,
    organizations: [
      {
        ...org,
        roles: role === 'resident' ? [] : org.roles,
        permissions: [...org.permissions, ...permissions.map((permission) => ({ permission }))],
      },
    ],
    occupancies,
  }
}

export async function startMock() {
  const role = new URLSearchParams(globalThis.location.search).get('mockRole')
  const worker = setupWorker(
    http.get('*/.well-known/openbekci.json', () => HttpResponse.json(discoveryFixture)),
    http.get('*/v1/me', () => HttpResponse.json(buildMe(role))),
    http.post('*/v1/auth/session', () => new HttpResponse(null, { status: 200 })),
    http.delete('*/v1/auth/session', () => new HttpResponse(null, { status: 204 })),
    ...governanceHandlers,
    ...communityHandlers,
    ...accessHandlers,
    ...directoryHandlers,
  )
  await worker.start({ onUnhandledFrame: 'bypass', quiet: true })
}
