import type { QueryClient } from '@tanstack/react-query'
import { setupWorker } from 'msw/browser'
import { handlers } from '@/test/handlers'
import { db, meByRoleHandler, type MockRole } from '@/test/handlers/operations'

/*
 * `pnpm dev:mock`: the app runs against the in-memory model of the draft contract (MSW service
 * worker), no backend needed. The signed-in role comes from `localStorage['ob.mockRole']` or
 * `window.__obMock.setRole(...)` (manager | staff | resident | platform); default manager.
 */

const ROLES: MockRole[] = ['manager', 'staff', 'resident', 'platform']

function storedRole(): MockRole {
  try {
    const v = localStorage.getItem('ob.mockRole') as MockRole | null
    if (v && ROLES.includes(v)) return v
  } catch {
    /* ignore */
  }
  return 'manager'
}

export async function startMocks() {
  db.role = storedRole()
  const worker = setupWorker(meByRoleHandler, ...handlers)
  await worker.start({
    onUnhandledFrame: 'bypass',
    serviceWorker: { url: '/mockServiceWorker.js' },
    quiet: true,
  })
}

declare global {
  interface Window {
    __obMock?: { setRole: (role: MockRole) => Promise<void>; role: () => MockRole }
  }
}

/** Switch the mock user without a reload (the in-memory data is kept). */
export function installMockControls(queryClient: QueryClient) {
  window.__obMock = {
    role: () => db.role,
    setRole: async (role) => {
      db.role = role
      try {
        localStorage.setItem('ob.mockRole', role)
      } catch {
        /* ignore */
      }
      await queryClient.invalidateQueries()
    },
  }
}
