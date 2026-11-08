import '@testing-library/jest-dom/vitest'
import { afterEach } from 'vitest'
import { cleanup, configure } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { setupServer } from 'msw/node'
import { handlers } from './handlers'
import { runResets } from './reset'

export const server = setupServer(...handlers)

// jsdom has no matchMedia; app/theme.ts reads it at import time.
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
}

// the whole route tree is transformed on first import; give async queries room on a busy machine
configure({ asyncUtilTimeout: 5000 })

i18n.load('en', {})
i18n.activate('en')

beforeAll(() => server.listen({ onUnhandledFrame: 'error' }))
afterEach(() => {
  cleanup()
  server.resetHandlers()
  runResets()
})
afterAll(() => server.close())
