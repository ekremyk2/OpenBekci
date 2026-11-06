import { expect, test, type Page } from '@playwright/test'

async function mockApi(page: Page) {
  await page.route('**/.well-known/openbekci.json', (route) =>
    route.fulfill({
      json: {
        instance_id: '0194a1f0-0000-7000-8000-000000000001',
        instance_name: 'Smoke Instance',
        api_base_url: 'http://localhost:5174/v1',
        api_versions: ['v1'],
        server_version: '0.1.0',
        sdk_version: null,
        auth_methods: [{ id: 'password' }],
        packs: [],
      },
    }),
  )
  await page.route('**/v1/me', (route) =>
    route.fulfill({ status: 401, contentType: 'application/problem+json', body: '{"status":401}' }),
  )
}

test('pseudo-rtl locale pseudolocalizes strings and flips the document direction', async ({
  page,
}) => {
  await mockApi(page)
  await page.goto('/login?locale=pseudo-rtl')
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl')
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await expect(page.getByText('Sign in to manage your building.')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Smoke Instance' })).toBeVisible()
})

test('Turkish locale translates the login page', async ({ page }) => {
  await mockApi(page)
  await page.goto('/login?locale=tr')
  await expect(page.getByText('Binanızı yönetmek için giriş yapın.')).toBeVisible()
  await expect(page.locator('html')).toHaveAttribute('lang', 'tr')
})
