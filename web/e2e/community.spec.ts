import { expect, test } from '@playwright/test'

// Runs against the mock-mode dev server (in-memory MSW handlers): the whole flow chains without a
// backend. Navigation inside a flow is always by clicking, so the in-memory state survives.

test('call a meeting, check in, record the counts and apply the elected role', async ({ page }) => {
  await page.goto('/orgs/org-1/meetings')
  await page.getByRole('link', { name: 'Elections 2027' }).click()
  await expect(page.getByRole('heading', { name: 'Elections 2027' })).toBeVisible()

  // agenda: one voted item whose topic decides the threshold
  await page.getByRole('tab', { name: 'Agenda' }).click()
  await page.getByRole('button', { name: 'Add an item' }).click()
  await page.getByLabel('Title').fill('Elect the site manager')
  await page.getByLabel('Topic').selectOption({ label: 'Election of the manager' })
  await page.getByRole('button', { name: 'Add item' }).click()
  await expect(page.getByRole('list', { name: 'Agenda' }).getByText('Elect the site manager')).toBeVisible()

  // call: legal timing is fine, the checklist appears
  await page.getByRole('tab', { name: 'Call' }).click()
  await page.getByRole('button', { name: 'Call the assembly' }).click()
  await expect(page.getByText('0 of 6 delivered')).toBeVisible()

  await page.getByRole('button', { name: 'Start the assembly' }).click()
  await page.getByRole('link', { name: 'Attendance desk' }).click()

  // desk: QR for one owner, ticks for the rest
  const meetingId = page.url().split('/meetings/')[1]!.split('/')[0]!
  await page.getByLabel('QR code').fill(`obk1.att.${meetingId}.p-ayse`)
  await page.getByRole('button', { name: 'Check', exact: true }).click()
  await expect(page.getByText('1 of 6 owners (16.7%)')).toBeVisible()
  for (const name of ['Mehmet Demir', 'Zeynep Kaya', 'Can Öztürk']) {
    await page.getByRole('button', { name: `Mark ${name} present` }).click()
  }
  await expect(page.getByText('4 of 6 owners (66.7%)')).toBeVisible()
  await expect(page.getByText('Quorum reached')).toBeVisible()
  await page.getByRole('link', { name: 'Back to the assembly' }).click()

  // counts, decision, apply
  await page.getByRole('tab', { name: 'Counts and decisions' }).click()
  await page.getByLabel('Yes', { exact: true }).fill('5')
  await page.getByLabel('No', { exact: true }).fill('1')
  await page.getByLabel('Abstain').fill('0')
  await page.getByLabel('Yes share of land (%)').fill('80')
  await page.getByRole('button', { name: 'Save counts' }).click()
  await expect(page.getByTestId('threshold-evaluation')).toContainText('met')
  await page.getByRole('button', { name: 'Record decision' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Record', exact: true }).click()
  await expect(page.getByText(/Accepted/).first()).toBeVisible()

  await page.getByRole('button', { name: 'Apply effect' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Elected person').selectOption({ label: 'Ayşe Yılmaz' })
  await dialog.getByRole('button', { name: 'Review' }).click()
  await dialog.getByRole('button', { name: 'Confirm and apply' }).click()
  await expect(page.getByText('Applied: Elected role with a term')).toBeVisible()
})

test('pre-register a visitor, the guard scans the code and admits them', async ({ page }) => {
  await page.goto('/orgs/org-1/visitors')
  await page.getByRole('button', { name: /Pre-register a visitor/ }).click()
  await page.getByLabel("Visitor's name").fill('Deniz Acar')
  await page.getByRole('button', { name: 'Create the pass' }).click()
  const code = (await page.getByTestId('qr-code-text').textContent())!.trim()
  expect(code).toMatch(/^obk1\./)
  await page.getByRole('button', { name: 'Done' }).click()

  await page.getByRole('link', { name: 'Gate', exact: true }).click()
  await page.getByRole('button', { name: 'Hasan Çelik' }).click()
  await page.getByLabel('PIN').fill('1234')
  await page.getByRole('button', { name: 'Start the shift' }).click()
  await expect(page.getByTestId('guard-name')).toHaveText('Hasan Çelik')

  await page.getByLabel('QR code').fill(code)
  await page.getByRole('button', { name: 'Check', exact: true }).click()
  await expect(page.getByText('Valid pass')).toBeVisible()
  await page.getByRole('status').filter({ hasText: 'Valid pass' }).getByRole('button', { name: 'Admit' }).click()
  await expect(page.getByText('Deniz Acar admitted.')).toBeVisible()
})
