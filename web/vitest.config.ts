import { defineConfig, mergeConfig } from 'vitest/config'
import viteConfig from './vite.config.ts'

export default defineConfig((env) =>
  mergeConfig(
    viteConfig(env),
    defineConfig({
      server: { fs: { allow: ['..'] } },
      test: {
        environment: 'jsdom',
        globals: true,
        setupFiles: ['./src/test/setup.ts'],
        include: ['src/**/*.test.{ts,tsx}'],
        css: false,
        testTimeout: 15_000,
      },
    }),
  ),
)
