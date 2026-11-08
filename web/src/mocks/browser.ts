import { setupWorker } from 'msw/browser'
import { handlers } from '@/test/handlers'
import { adminHandlers } from '@/test/handlers/me'

/**
 * `pnpm dev:mock`: the API is answered in the browser by MSW from the draft contract's examples
 * (src/test/handlers), so the setup and people screens can be demoed without a server. You are
 * signed in as an organization admin with every permission; data lives in memory and resets on
 * reload. Imported only when `import.meta.env.MODE === 'mock'` (see main.tsx).
 */
export async function startMocks() {
  const worker = setupWorker(...adminHandlers(), ...handlers)
  await worker.start({
    onUnhandledFrame: 'bypass',
    serviceWorker: { url: '/mockServiceWorker.js' },
    quiet: true,
  })
}
