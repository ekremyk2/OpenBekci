import { expect, test } from '@playwright/test'

/*
 * Packs, API keys and webhooks in mock mode: the API is answered by MSW in the browser from the
 * draft contract (src/test/handlers/integrations.ts). Signed in as an organization admin.
 */

test('enable a pack with its schema-driven settings form', async ({ page }) => {
  await page.goto('/orgs/org-1/settings/packs?locale=en')
  await expect(page.getByRole('heading', { name: 'Packs', level: 2 })).toBeVisible()
  await page.getByRole('link', { name: 'Set up and enable tr' }).click()

  // groups and field order come from x-ui, the secret is write-only
  await expect(page.getByRole('group', { name: 'SMS' })).toBeVisible()
  await expect(page.getByText('Not set')).toBeVisible()
  await page.getByRole('button', { name: 'Enable pack' }).click()
  await expect(page.getByText('This field is required').first()).toBeVisible()

  await page.getByLabel(/SMS provider/).selectOption('netgsm')
  await page.getByLabel(/Sms api key/).fill('k-1234567890')
  await page.getByLabel(/Sms sender/).fill('BEKCI')
  await page.getByRole('button', { name: 'Enable pack' }).click()

  await expect(page.getByRole('heading', { name: 'Organization settings' })).toBeVisible()
  await expect(page.getByText('Enabled', { exact: true }).first()).toBeVisible()
  // the secret is never read back: the field is empty and marked set
  await expect(page.getByLabel(/Sms api key/)).toHaveValue('')
  await expect(page.getByText('Set', { exact: true })).toBeVisible()
  // the presets of the pack filled the site settings
  await expect(page.getByText('Pack defaults on your sites')).toBeVisible()
  await expect(page.getByText('late_fee_policy')).toBeVisible()

  // disable with a confirmation
  await page.getByRole('button', { name: 'Disable pack' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Disable pack' }).click()
  await expect(page.getByText('Enable this pack')).toBeVisible()
})

test('create an API key through re-authentication and see the secret once', async ({ page }) => {
  await page.goto('/orgs/org-1/settings/api-keys?locale=en')
  await expect(page.getByText('Accounting export')).toBeVisible()
  await page.getByRole('button', { name: 'Create API key' }).click()
  const dialog = page.getByRole('dialog', { name: 'Create API key' })
  await dialog.getByLabel('Name').fill('CI export')
  await dialog.getByRole('checkbox', { name: 'tickets.read' }).check()
  await dialog.getByRole('button', { name: 'Create key' }).click()

  // 403 reauth_required: confirm with the password, the request is replayed
  const reauth = page.getByRole('dialog', { name: 'Confirm it is you' })
  await reauth.getByLabel('Password').fill('correct-password')
  await reauth.getByRole('button', { name: 'Confirm' }).click()

  const reveal = page.getByRole('dialog', { name: 'Your new API key' })
  await expect(reveal.getByLabel('The secret')).toHaveValue(/^obk_[a-z0-9]{8}_/)
  await expect(reveal.getByRole('button', { name: 'Done' })).toBeDisabled()
  await reveal.getByRole('checkbox').check()
  await reveal.getByRole('button', { name: 'Done' }).click()
  await expect(reveal).toBeHidden()
  await expect(page.getByRole('row', { name: /CI export/ })).toBeVisible()
})

test('create a webhook endpoint and send a test ping', async ({ page }) => {
  await page.goto('/orgs/org-1/settings/webhooks?locale=en')
  await page.getByRole('button', { name: 'Add endpoint' }).click()
  const dialog = page.getByRole('dialog', { name: 'Add endpoint' })
  await dialog.getByLabel('URL').fill('https://hooks.example.org/helpdesk')
  await dialog.getByRole('checkbox', { name: /ticket\.created/ }).check()
  await dialog.getByRole('button', { name: 'Create endpoint' }).click()

  const reveal = page.getByRole('dialog', { name: 'Signing secret' })
  await expect(reveal.getByLabel('The secret')).toHaveValue(/^whsec_/)
  await reveal.getByRole('checkbox').check()
  await reveal.getByRole('button', { name: 'Done' }).click()

  await page
    .getByRole('button', { name: 'Send a test ping to https://hooks.example.org/helpdesk' })
    .click()
  const result = page.getByRole('dialog', { name: 'Test ping sent' })
  await expect(result.getByText('Succeeded')).toBeVisible()
  await result.getByRole('button', { name: 'Show deliveries' }).click()
  await expect(page.getByRole('region', { name: /Deliveries of/ }).getByText('ping')).toBeVisible()
})
