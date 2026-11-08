import { fileURLToPath } from 'node:url'
import { expect, test } from '@playwright/test'

/*
 * Whole setup-and-people flow in mock mode: the API is answered by MSW in the browser from the draft
 * contract (src/test/handlers), so no server is needed. Signed in as an organization admin.
 */
const unitsCsv = fileURLToPath(new URL('../fixtures/units.csv', import.meta.url))

test('create a site, import its units, invite a person, approve a claim', async ({ page }) => {
  // 1. create a site
  await page.goto('/orgs/org-1/sites/new?locale=en')
  await page.getByLabel('Site name').fill('Deniz Sitesi')
  await page.getByLabel('Street address').fill('Bağdat Cd. 7')
  await page.getByRole('button', { name: 'Create site' }).click()
  await expect(page.getByRole('heading', { name: 'Deniz Sitesi' })).toBeVisible()

  // 2. import units from the fixture CSV: columns are guessed, the check passes, the job finishes
  await page.getByRole('link', { name: 'Units', exact: true }).first().click()
  await page.getByRole('link', { name: 'Import units' }).click()
  await page.getByLabel('Site', { exact: true }).selectOption({ label: 'Deniz Sitesi' })
  await page.getByLabel('Spreadsheet file').setInputFiles(unitsCsv)
  await expect(page.getByLabel(/Door number/)).toHaveValue('Daire No')
  await page.getByRole('button', { name: 'Check the file' }).click()
  await expect(page.getByText('3 fine')).toBeVisible()
  await page.getByRole('button', { name: 'Import 3 rows' }).click()
  await expect(page.getByText('Import finished')).toBeVisible({ timeout: 15_000 })

  await page.getByRole('link', { name: 'Units', exact: true }).first().click()
  await page.getByLabel('Site', { exact: true }).selectOption({ label: 'Deniz Sitesi' })
  await expect(page.getByRole('link', { name: 'D-2' })).toBeVisible()

  // 3. invite a person by SMS and see the invitation waiting
  await page.getByRole('link', { name: 'People', exact: true }).first().click()
  await page.getByRole('link', { name: 'Add a person' }).click()
  await page.getByLabel('Full name').fill('Selin Aydın')
  await page.getByLabel('Phone', { exact: true }).fill('+90 555 000 11 22')
  await page.getByRole('button', { name: 'Add person' }).click()
  await page.getByRole('button', { name: 'Invite', exact: true }).click()
  await page.getByRole('button', { name: 'Send invitation' }).click()
  await expect(page.getByText('+90 555 000 11 22')).toBeVisible()
  await expect(page.getByText('Waiting')).toBeVisible()

  // 4. approve a unit claim from the site QR sign-up
  await page.goto('/orgs/org-1/claims?locale=en')
  const row = page.getByRole('row', { name: /Burak Şahin/ })
  await row.getByRole('button', { name: 'Approve' }).click()
  await page.getByLabel('Ownership share (%)').fill('100')
  await page.getByRole('button', { name: 'Approve claim' }).click()
  await expect(page.getByRole('row', { name: /Burak Şahin/ })).toBeHidden()
})
