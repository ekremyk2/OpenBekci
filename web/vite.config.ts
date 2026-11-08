import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import babel from '@rolldown/plugin-babel'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import { lingui } from '@lingui/vite-plugin'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import type { Plugin } from 'vite'

/**
 * Serves MSW's worker script at `/mockServiceWorker.js` for the dev server only. It lives outside
 * `public/` so it is never copied into `dist/`: the production build ships no mocking code.
 */
function mockServiceWorker(): Plugin {
  const file = fileURLToPath(new URL('./src/mocks/sw/mockServiceWorker.js', import.meta.url))
  return {
    name: 'openbekci:mock-service-worker',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use('/mockServiceWorker.js', (_req, res) => {
        res.setHeader('Content-Type', 'text/javascript; charset=utf-8')
        res.setHeader('Cache-Control', 'no-cache')
        res.end(readFileSync(file))
      })
    },
  }
}

const backend = process.env.OPENBEKCI_DEV_BACKEND ?? 'http://localhost:8484'

export default defineConfig(({ mode }) => ({
  plugins: [
    tanstackRouter({ target: 'react', autoCodeSplitting: true }),
    react(),
    babel({ plugins: ['@lingui/babel-plugin-lingui-macro'] }),
    lingui(),
    tailwindcss(),
    mockServiceWorker(),
  ],
  // `--mode mock` (or `--mode=mock`) runs next to the normal dev server (e2e): its own dependency
  // cache, or the two optimizers invalidate each other's chunks mid-test
  cacheDir: mode === 'mock' ? 'node_modules/.vite-mock' : 'node_modules/.vite',
  // lazily loaded dependencies: pre-bundled up front so the dev server never re-optimizes (and
  // reloads the page) the first time a lazy chunk is opened
  optimizeDeps: { include: ['react-markdown', 'msw/browser'] },
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  // The Go server replaces __CSP_NONCE__ with a per-response nonce (backend §16).
  html: { cspNonce: '__CSP_NONCE__' },
  server: {
    port: 5173,
    proxy: {
      '/v1': { target: backend, changeOrigin: false },
      '/.well-known': { target: backend, changeOrigin: false },
    },
  },
  build: { outDir: 'dist', sourcemap: false, target: 'es2023' },
}))
