import { expect, test, type Page } from '@playwright/test'

// Runs against `vite --mode mock` (port 5175, see playwright.config.ts): the app talks to the
// in-memory model of the draft operations contract through an MSW service worker.
test.use({ baseURL: 'http://localhost:5175' })

const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
  'base64',
)

async function signInAs(page: Page, role: 'resident' | 'staff' | 'manager') {
  await page.addInitScript((r) => {
    localStorage.setItem('ob.locale', 'en')
    localStorage.setItem('ob.mockRole', r)
  }, role)
}

async function switchRole(page: Page, role: 'resident' | 'staff' | 'manager') {
  await page.evaluate(
    (r) =>
      (
        globalThis as unknown as { __obMock: { setRole(r: string): Promise<void> } }
      ).__obMock.setRole(r),
    role,
  )
}

test('resident opens a ticket with a photo, staff changes the status, the resident sees it', async ({
  page,
}) => {
  await signInAs(page, 'resident')
  await page.goto('/orgs/org-1/tickets/new')

  await page.getByLabel('Category').selectOption({ label: 'Elevator' })
  await page.getByLabel('Title').fill('Elevator door does not close')
  await page.getByLabel('Description').fill('The door stays open on the 3rd floor.')
  await page.locator('input[type=file]').setInputFiles({
    name: 'door.png',
    mimeType: 'image/png',
    buffer: PNG,
  })
  await expect(page.getByText('Uploaded')).toBeVisible()
  await page.getByRole('button', { name: 'Open ticket' }).click()

  await expect(page.getByRole('heading', { name: 'Elevator door does not close' })).toBeVisible()
  await expect(page.getByRole('button', { name: /Open photo 1 of 1/ })).toBeVisible()
  await expect(page.getByText('Open', { exact: true }).first()).toBeVisible()

  // staff takes over (same in-memory data, no reload)
  await switchRole(page, 'staff')
  await expect(page.getByLabel('Change status')).toBeVisible()
  await page.getByLabel('Change status').selectOption({ label: 'In progress' })
  await page.getByLabel('Note with the status change').fill('Technician comes tomorrow')
  await page.getByRole('button', { name: 'Update status' }).click()
  await expect(page.getByText(/Status changed from Open to In progress/)).toBeVisible()

  // the resident sees the new status and the note
  await switchRole(page, 'resident')
  await expect(page.getByLabel('Change status')).toHaveCount(0)
  await expect(page.getByText('Technician comes tomorrow')).toBeVisible()
  await expect(page.getByText('In progress').first()).toBeVisible()

  // the photo opens in the lightbox
  await page.getByRole('button', { name: /Open photo 1 of 1/ }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
})

test('manager sees the SMS count before publishing an announcement', async ({ page }) => {
  await signInAs(page, 'manager')
  await page.goto('/orgs/org-1/announcements/new')

  await page.getByLabel('Title').fill('Water cut on Saturday')
  await page.getByRole('textbox', { name: /Message/ }).fill('No water between 10:00 and 14:00.')
  const preview = page.getByRole('status', { name: 'Delivery preview' })
  await expect(preview).toContainText('Recipients: 120')
  await expect(preview).not.toContainText('SMS messages')

  await page.getByRole('checkbox', { name: 'SMS' }).check()
  await expect(preview).toContainText('SMS messages: 108 (billed to the site)')

  await page.getByRole('button', { name: 'Publish' }).click()
  await expect(page.getByRole('heading', { name: 'Water cut on Saturday' })).toBeVisible()
  await expect(page.getByText('Sent to 120 people, 108 SMS messages.')).toBeVisible()
  await expect(page.getByText('Read receipts')).toBeVisible()
})
