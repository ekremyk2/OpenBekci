import { defineConfig, devices } from '@playwright/test'

// Smoke tests run against the Vite dev server with the API mocked in the tests themselves, so no
// backend is needed. Browsers: `pnpm exec playwright install chromium` once.
// The `money` project runs against a second dev server in mock mode (`vite --mode mock`): the
// in-memory MSW handlers answer, so a whole money flow chains without a backend.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: { baseURL: 'http://localhost:5174', trace: 'on-first-retry' },
  projects: [
    { name: 'chromium', testIgnore: /money\.spec/, use: { ...devices['Desktop Chrome'] } },
    {
      name: 'money',
      testMatch: /money\.spec/,
      fullyParallel: false,
      use: { ...devices['Desktop Chrome'], baseURL: 'http://localhost:5175' },
    },
  ],
  webServer: [
    {
      command: 'pnpm exec vite --port 5174 --strictPort',
      url: 'http://localhost:5174',
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
    {
      command:
        'pnpm exec msw init public --no-save && pnpm exec vite --mode mock --port 5175 --strictPort',
      url: 'http://localhost:5175',
      reuseExistingServer: !process.env.CI,
      timeout: 90_000,
    },
  ],
})
