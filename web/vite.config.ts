import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import babel from '@rolldown/plugin-babel'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import { lingui } from '@lingui/vite-plugin'
import { fileURLToPath } from 'node:url'

const backend = process.env.OPENBEKCI_DEV_BACKEND ?? 'http://localhost:8484'

export default defineConfig({
  plugins: [
    tanstackRouter({ target: 'react', autoCodeSplitting: true }),
    react(),
    babel({ plugins: ['@lingui/babel-plugin-lingui-macro'] }),
    lingui(),
    tailwindcss(),
  ],
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
