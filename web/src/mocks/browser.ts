import { setupWorker } from 'msw/browser'
import { handlers } from '@/test/handlers'
import { http, HttpResponse } from 'msw'
import { adminHandlers, roleMe } from '@/test/handlers/me'

/**
 * `pnpm dev:mock`: the API is answered in the browser by MSW from the draft contract's examples
 * (src/test/handlers), so every screen can be demoed without a server. You are signed in as an
 * organization admin with every permission; data lives in memory and resets on reload. Open the
 * app with `?mockRole=resident` for the resident view (money portal, assemblies, bookings) or
 * `?mockRole=guard` for the guard of the booth (gate console; the guard PIN is 1234). Imported only
 * when `import.meta.env.MODE === 'mock'` (see main.tsx).
 */
export async function startMocks() {
  const role = new URLSearchParams(globalThis.location.search).get('mockRole')
  const worker = setupWorker(
    ...(role === 'resident' || role === 'guard'
      ? [http.get('*/v1/me', () => HttpResponse.json(roleMe(role)))]
      : []),
    ...adminHandlers(),
    ...handlers,
  )
  await worker.start({
    onUnhandledFrame: 'bypass',
    serviceWorker: { url: '/mockServiceWorker.js' },
    quiet: true,
  })
}
