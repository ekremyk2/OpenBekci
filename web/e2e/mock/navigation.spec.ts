import { expect, test } from '@playwright/test'

// Runs in the `mock` project. Every role walks every sidebar entry: no 403, no blank page, and a
// detail page always has a Back that leads somewhere usable.
const ROLES = ['admin', 'manager', 'staff', 'resident', 'guard'] as const

for (const role of ROLES) {
  test(`${role}: every nav entry opens a real page`, async ({ page }) => {
    test.setTimeout(120_000) // the first load of a cold dev server compiles every route
    await page.addInitScript((r) => {
      localStorage.setItem('ob.locale', 'en')
      if (r === 'manager' || r === 'staff') localStorage.setItem('ob.mockRole', r)
    }, role)
    const q = role === 'resident' || role === 'guard' ? `&mockRole=${role}` : ''
    await page.goto(`/orgs/org-1?locale=en${q}`)
    if (role === 'guard') {
      // the guard lands on the gate console, a full-screen kiosk that covers the app chrome
      await expect(page.getByTestId('guard-console')).toBeVisible({ timeout: 60_000 })
      await expect(page).toHaveURL(/\/visitors\/gate/)
      return
    }
    const nav = page.getByRole('navigation', { name: 'Main' })
    await expect(nav.getByRole('link').first()).toBeVisible({ timeout: 60_000 })
    const labels = await nav.getByRole('link').allInnerTexts()
    expect(labels.length).toBeGreaterThan(0)
    for (const label of labels) {
      await nav.getByRole('link', { name: label.trim(), exact: true }).click()
      const main = page.locator('main')
      await expect(main).not.toContainText('You do not have access')
      await expect(main).not.toContainText('Page not found')
      await expect(main.locator('h1, h2').first()).toBeVisible({ timeout: 30_000 })
    }
  })
}

test('a detail page has Back: history when there is some, the list when there is none', async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem('ob.locale', 'en'))
  await page.goto('/orgs/org-1/sites/new?locale=en')
  await page.getByRole('button', { name: 'Back' }).click()
  await expect(page).toHaveURL(/\/orgs\/org-1\/sites$/)

  await page.getByRole('link', { name: 'Units' }).click()
  await page.goto('/orgs/org-1/people/new?locale=en')
  await page.getByRole('button', { name: 'Back' }).click()
  await expect(page).toHaveURL(/\/orgs\/org-1\/people$/)
})
