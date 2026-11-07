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
  projects: [
    // API mocked per test with page.route (dev server on :5174)
    { name: 'chromium', testIgnore: /mock\//, use: { ...devices['Desktop Chrome'] } },
    // API answered by MSW in the browser (`vite --mode mock`, :5175): whole flows without a server
    {
      name: 'mock',
      testMatch: /mock\/.*\.spec\.ts/,
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
      command: 'pnpm exec vite --mode mock --port 5175 --strictPort',
      url: 'http://localhost:5175',
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
  ],
})
