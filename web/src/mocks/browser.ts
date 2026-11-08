import { setupWorker } from 'msw/browser'
import { handlers } from '@/test/handlers'
import { http, HttpResponse } from 'msw'
import { adminHandlers, residentMe } from '@/test/handlers/me'

/**
 * `pnpm dev:mock`: the API is answered in the browser by MSW from the draft contract's examples
 * (src/test/handlers), so the setup and people screens can be demoed without a server. You are
 * signed in as an organization admin with every permission; data lives in memory and resets on
 * reload. Open the app with `?mockRole=resident` for the resident (money portal) view. Imported only when `import.meta.env.MODE === 'mock'` (see main.tsx).
 */
export async function startMocks() {
  const resident = new URLSearchParams(globalThis.location.search).get('mockRole') === 'resident'
  const worker = setupWorker(
    ...(resident ? [http.get('*/v1/me', () => HttpResponse.json(residentMe()))] : []),
    ...adminHandlers(),
    ...handlers,
  )
  await worker.start({
    onUnhandledFrame: 'bypass',
    serviceWorker: { url: '/mockServiceWorker.js' },
    quiet: true,
  })
}
