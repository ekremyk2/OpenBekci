import { http, HttpResponse } from 'msw'
import { setupWorker } from 'msw/browser'
import type { Me } from '@/api/schema'
import { discoveryFixture, meFixture } from '@/test/handlers'
import { moneyHandlers } from '@/test/handlers/money'

/**
 * `pnpm dev:mock`: the app runs against in-memory handlers, no backend needed. The signed-in user
 * is a site manager with every money permission; open the app with `?mockRole=resident` for the
 * resident (portal) view of unit A1.
 */
const MONEY_PERMISSIONS = [
  'money.read',
  'money.post',
  'money.manage',
  'money.lock',
  'payments.record',
  'collections.manage',
  'statements.read',
  'reports.read',
  'reports.debtors',
  'reports.aggregate',
]

function buildMe(role: string | null): Me {
  const org = meFixture.organizations[0]!
  if (role === 'resident') {
    return {
      ...meFixture,
      organizations: [
        {
          ...org,
          roles: [],
          permissions: [{ permission: 'statements.read_own' }, { permission: 'payments.declare' }],
        },
      ],
    }
  }
  return {
    ...meFixture,
    organizations: [
      {
        ...org,
        permissions: [
          ...org.permissions,
          ...MONEY_PERMISSIONS.map((permission) => ({ permission })),
        ],
      },
    ],
  }
}

export async function startMock() {
  const role = new URLSearchParams(globalThis.location.search).get('mockRole')
  const worker = setupWorker(
    http.get('*/.well-known/openbekci.json', () => HttpResponse.json(discoveryFixture)),
    http.get('*/v1/me', () => HttpResponse.json(buildMe(role))),
    http.post('*/v1/auth/session', () => new HttpResponse(null, { status: 200 })),
    http.delete('*/v1/auth/session', () => new HttpResponse(null, { status: 204 })),
    ...moneyHandlers,
  )
  await worker.start({ onUnhandledFrame: 'bypass', quiet: true })
}
