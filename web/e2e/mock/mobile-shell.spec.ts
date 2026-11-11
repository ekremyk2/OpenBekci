import { expect, test } from '@playwright/test'

// Runs in the `mock` project. At 390 px the header collapses and no route scrolls sideways.
const ROUTES = ['', '/money', '/tickets', '/settings', '/sites', '/people', '/me']

test('390 px: no route scrolls horizontally', async ({ page }) => {
  test.setTimeout(120_000)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.addInitScript(() => localStorage.setItem('ob.locale', 'en'))
  for (const route of ROUTES) {
    await page.goto(`/orgs/org-1${route}?locale=en`)
    await expect(page.locator('main h1').first()).toBeVisible({ timeout: 60_000 })
    const [scroll, inner] = (await page.evaluate(
      '[document.documentElement.scrollWidth, window.innerWidth]',
    )) as [number, number]
    expect(scroll, route || 'home').toBeLessThanOrEqual(inner)
  }
  await expect(page.getByRole('button', { name: 'Sign out' })).toBeInViewport()
})
