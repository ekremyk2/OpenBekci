import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import babel from '@rolldown/plugin-babel'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import { lingui } from '@lingui/vite-plugin'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const backend = process.env.OPENBEKCI_DEV_BACKEND ?? 'http://localhost:8484'

export default defineConfig({
  plugins: [
    tanstackRouter({ target: 'react', autoCodeSplitting: true }),
    react(),
    babel({ plugins: ['@lingui/babel-plugin-lingui-macro'] }),
    lingui(),
    tailwindcss(),
    {
      // `vite --mode mock` (pnpm dev:mock): serve the MSW worker script; never part of a build
      name: 'openbekci-mock-worker',
      apply: (_config, { mode, command }) => mode === 'mock' && command === 'serve',
      configureServer(server) {
        server.middlewares.use('/mockServiceWorker.js', (_req, res) => {
          res.setHeader('Content-Type', 'text/javascript')
          res.end(
            readFileSync(
              fileURLToPath(
                new URL('./node_modules/msw/lib/mockServiceWorker.js', import.meta.url),
              ),
            ),
          )
        })
      },
    },
  ],
  // `--mode mock` runs next to the normal dev server (e2e): its own dependency cache, or the two
  // optimizers invalidate each other's chunks mid-test
  cacheDir: process.argv.some((a, i) => a === 'mock' && process.argv[i - 1] === '--mode')
    ? 'node_modules/.vite-mock'
    : 'node_modules/.vite',
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
})
