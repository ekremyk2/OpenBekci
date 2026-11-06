import { expect, test } from '@playwright/test'

const discovery = {
  instance_id: '0194a1f0-0000-7000-8000-000000000001',
  instance_name: 'Smoke Instance',
  api_base_url: 'http://localhost:5174/v1',
  api_versions: ['v1'],
  server_version: '0.1.0',
  sdk_version: null,
  auth_methods: [{ id: 'password' }, { id: 'sms_otp' }, { id: 'oidc:google' }],
  packs: [],
}

test('login page renders sign-in methods from the discovery document', async ({ page }) => {
  await page.route('**/.well-known/openbekci.json', (route) => route.fulfill({ json: discovery }))
  await page.route('**/v1/me', (route) =>
    route.fulfill({
      status: 401,
      contentType: 'application/problem+json',
      body: JSON.stringify({ status: 401, code: 'invalid_grant' }),
    }),
  )

  await page.goto('/login?locale=en')

  await expect(page.getByRole('heading', { name: 'Smoke Instance' })).toBeVisible()
  // phone (SMS) first, then password; magic link is absent because the instance does not offer it
  const tabs = page.getByRole('tab')
  await expect(tabs).toHaveText(['SMS code', 'Password'])
  await expect(page.getByLabel('Phone number')).toBeVisible()
  await page.getByRole('tab', { name: 'Password' }).click()
  await expect(page.getByLabel('Email or phone')).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Password' })).toBeVisible()
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
})

test('discovery branding and instance name reach the page', async ({ page }) => {
  await page.route('**/.well-known/openbekci.json', (route) =>
    route.fulfill({
      json: { ...discovery, instance_name: 'Brand Test', branding: { primary_color: '#b45309' } },
    }),
  )
  await page.route('**/v1/me', (route) =>
    route.fulfill({ status: 401, contentType: 'application/problem+json', body: '{"status":401}' }),
  )
  await page.goto('/login?locale=en')
  await expect(page).toHaveTitle('Brand Test')
  await expect(page.getByRole('heading', { name: 'Brand Test' })).toBeVisible()
  const seed = await page.evaluate(() =>
    document.documentElement.style.getPropertyValue('--brand-seed'),
  )
  expect(seed).toBe('#b45309')
})

test('unauthenticated visit to an org route redirects to login with next', async ({ page }) => {
  await page.route('**/.well-known/openbekci.json', (route) => route.fulfill({ json: discovery }))
  await page.route('**/v1/me', (route) =>
    route.fulfill({ status: 401, contentType: 'application/problem+json', body: '{"status":401}' }),
  )
  await page.goto('/orgs/abc/me?locale=en')
  await expect(page).toHaveURL(/\/login\?next=%2Forgs%2Fabc%2Fme/)
})
