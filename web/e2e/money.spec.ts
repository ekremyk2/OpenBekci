import { expect, test } from '@playwright/test'

// Runs against `vite --mode mock`: in-memory money handlers, signed-in site manager.

test('dues plan with live preview, then a cash payment, then the statement', async ({ page }) => {
  await page.goto('/orgs/org-1/money/plans/new?locale=en')

  // 1. new dues plan: the preview table shows what every unit side would pay before saving
  await expect(page.getByRole('heading', { name: 'New dues plan' })).toBeVisible()
  await page.getByLabel('Name').fill('Garden fund')
  await page.getByLabel('Amount per period').fill('3000')
  const preview = page.getByRole('table', { name: 'What each unit side would pay' })
  await expect(preview).toBeVisible()
  await expect(preview.getByRole('row')).toHaveCount(7)
  await page.getByRole('button', { name: 'Save plan' }).click()
  await expect(page.getByRole('heading', { name: 'Garden fund' })).toBeVisible()

  // 2. record a cash payment for A3 (owner side); it is allocated oldest first and gets receipt no. 1
  await page.goto('/orgs/org-1/money/payments/new?locale=en')
  await page.getByLabel('Unit').selectOption({ label: 'A3' })
  await page.getByLabel('Amount', { exact: true }).fill('900')
  await expect(page.getByRole('table', { name: 'Automatic allocation' })).toBeVisible()
  await page.getByRole('button', { name: 'Record payment' }).click()
  await expect(page.getByTestId('receipt-no')).toHaveText(/\/000001$/)

  // 3. the statement of that unit side shows the payment
  await page.getByRole('link', { name: 'See statement' }).click()
  const statement = page.getByRole('table', { name: 'Statement' })
  await expect(statement).toBeVisible()
  await expect(statement.getByText(/Payment .*000001/)).toBeVisible()
})
