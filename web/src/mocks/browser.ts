import { setupWorker } from 'msw/browser'
import { handlers } from '@/test/handlers'
import { http, HttpResponse } from 'msw'
import type { QueryClient } from '@tanstack/react-query'
import { adminHandlers, roleMe } from '@/test/handlers/me'
import { db as opsDb, meForRole, type MockRole } from '@/test/handlers/operations'

/**
 * `pnpm dev:mock`: the API is answered in the browser by MSW from the draft contract's examples
 * (src/test/handlers), so every screen can be demoed without a server. You are signed in as an
 * organization admin with every permission; data lives in memory and resets on reload. Open the
 * app with `?mockRole=resident` for the resident view (money portal, assemblies, bookings) or
 * `?mockRole=guard` for the guard of the booth (gate console; the guard PIN is 1234). Imported only
 * when `import.meta.env.MODE === 'mock'` (see main.tsx).
 */
const OPS_ROLES: MockRole[] = ['manager', 'staff', 'resident', 'platform']

/**
 * The operations screens can also be driven as a manager, staff member, resident or platform admin:
 * `localStorage['ob.mockRole']` picks the user at load, `window.__obMock.setRole(...)` switches it
 * without a reload (the in-memory data is kept). `?mockRole=` in the URL wins (see above).
 */
let opsRole: MockRole | null = null
try {
  const stored = localStorage.getItem('ob.mockRole') as MockRole | null
  if (stored && OPS_ROLES.includes(stored)) opsRole = stored
} catch {
  /* storage blocked: stay the admin */
}

export async function startMocks() {
  const role = new URLSearchParams(globalThis.location.search).get('mockRole')
  if (opsRole) opsDb.role = opsRole
  const worker = setupWorker(
    ...(role === 'resident' || role === 'guard'
      ? [http.get('*/v1/me', () => HttpResponse.json(roleMe(role)))]
      : [http.get('*/v1/me', () => (opsRole ? HttpResponse.json(meForRole(opsRole)) : undefined))]),
    ...adminHandlers(),
    ...handlers,
  )
  await worker.start({
    onUnhandledFrame: 'bypass',
    serviceWorker: { url: '/mockServiceWorker.js' },
    quiet: true,
  })
}

declare global {
  interface Window {
    __obMock?: { setRole: (role: MockRole) => Promise<void>; role: () => MockRole | null }
  }
}

/** Switch the mock user without a reload. */
export function installMockControls(queryClient: QueryClient) {
  window.__obMock = {
    role: () => opsRole,
    setRole: async (next) => {
      opsRole = next
      opsDb.role = next
      try {
        localStorage.setItem('ob.mockRole', next)
      } catch {
        /* ignore */
      }
      await queryClient.invalidateQueries()
    },
  }
}
