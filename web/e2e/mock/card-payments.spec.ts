import { expect, test } from '@playwright/test'

// Runs in the `mock` project (`vite --mode mock`): the API is answered by MSW in the browser.
// The mock "payment provider" settles a checkout on the second status poll (paid by default).

test('a resident pays by card: redirect to the provider, back to the return page, paid', async ({
  page,
}) => {
  // The provider is another site: the navigation to it is intercepted and answered with a redirect
  // back to the return page, as the real provider does after the card was entered.
  await page.route('https://pay.example.test/**', async (route) => {
    const id = new URL(route.request().url()).pathname.split('/').pop()!
    await route.fulfill({
      status: 302,
      headers: {
        location: `http://localhost:5175/orgs/org-1/pay/return?checkout_id=${id}&locale=en&mockRole=resident`,
      },
    })
  })

  await page.goto('/orgs/org-1/money/pay?locale=en&mockRole=resident')
  await expect(page.getByRole('heading', { name: 'Pay', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Pay by card' }).first().click()

  const dialog = page.getByRole('dialog', { name: 'Pay by card' })
  await dialog.getByRole('radio', { name: 'An amount' }).check()
  await dialog.getByLabel('Amount', { exact: true }).fill('250')
  await dialog.getByRole('button', { name: 'Continue to payment' }).click()

  // full navigation to the provider, then back to the return page with the checkout id
  await page.waitForURL(/\/orgs\/org-1\/pay\/return\?checkout_id=co-/)
  await expect(page.getByText(/Confirming your payment/)).toBeVisible()
  await expect(page.getByText('Payment received')).toBeVisible({ timeout: 10_000 })
  await expect(page.getByRole('button', { name: 'Download receipt (PDF)' })).toBeVisible()

  // and it is in my list of card payments
  await expect(page.getByRole('link', { name: 'See my statement' })).toBeVisible()
  await page.goto('/orgs/org-1/money/my-checkouts?locale=en&mockRole=resident')
  const table = page.getByRole('table', { name: 'My card payments' })
  await expect(table.getByText('Paid').first()).toBeVisible()
})

test('a manager records a payout of client money to the site', async ({ page }) => {
  await page.goto('/orgs/org-1/money/settlements?locale=en')
  await expect(page.getByTestId('client-money-owed')).toContainText('400.00')
  await expect(page.getByTestId('clearing-iyzico')).toContainText('1,250.00')

  await page.getByRole('button', { name: 'Record payout to site' }).click()
  const dialog = page.getByRole('dialog', { name: 'Record payout to site' })
  await dialog.getByLabel('Bank transfer reference').fill('HAVALE-2026-11')
  await dialog.getByRole('button', { name: 'Record payout' }).click()

  await expect(page.getByTestId('client-money-owed')).toContainText('0.00')
  await expect(page.getByTestId('client-money-paid-out')).toContainText('400.00')
  await expect(page.getByRole('button', { name: 'Record payout to site' })).toBeHidden()
})

test('changing the merchant of record is blocked by balances, and says which', async ({ page }) => {
  await page.goto('/orgs/org-1/settings?locale=en')
  await page.getByLabel('Collected by').selectOption('organization')
  await page.getByRole('button', { name: 'Save' }).last().click()

  const blocked = page.getByRole('alert').filter({ hasText: 'cannot change yet' })
  await expect(blocked).toBeVisible()
  await expect(blocked.getByTestId('blocked-balance')).toHaveCount(2)
  await expect(blocked).toContainText('1,250.00')
  await expect(blocked).toContainText('400.00')

  // pay the client money out (a client-side visit keeps the mock state), then it lists one balance
  await blocked.getByRole('link', { name: /settlements and client money/ }).click()
  await page.getByRole('button', { name: 'Record payout to site' }).click()
  await page
    .getByRole('dialog', { name: 'Record payout to site' })
    .getByLabel('Bank transfer reference')
    .fill('HAVALE-1')
  await page.getByRole('button', { name: 'Record payout', exact: true }).click()
  await expect(page.getByTestId('client-money-owed')).toContainText('0.00')

  await page.goBack()
  await page.getByLabel('Collected by').selectOption('organization')
  await page.getByRole('button', { name: 'Save' }).last().click()
  await expect(page.getByTestId('blocked-balance')).toHaveCount(1)
})
