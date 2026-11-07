import { defineConfig, devices } from '@playwright/test'

// Smoke tests run against the Vite dev server with the API mocked in the tests themselves, so no
// backend is needed. Browsers: `pnpm exec playwright install chromium` once.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: { baseURL: 'http://localhost:5174', trace: 'on-first-retry' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: 'pnpm exec vite --port 5174 --strictPort',
      url: 'http://localhost:5174',
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
    {
      // mock mode: the whole app against the in-memory draft contract (src/mocks)
      command: 'pnpm exec vite --mode mock --port 5175 --strictPort',
      url: 'http://localhost:5175',
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
  ],
})
