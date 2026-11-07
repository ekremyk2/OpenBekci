import { i18n } from '@lingui/core'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { render } from '@testing-library/react'
import { vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import type { Me } from '@/api/schema'
import { Providers, createQueryClient } from '@/app/providers'
import { createAppRouter } from '@/app/router'
import { meFixture } from '@/test/handlers'
import { resetMoneyDb } from '@/test/handlers/money'
import { server } from '@/test/setup'

// jsdom's File/Blob cannot be serialised by Node's fetch (multipart uploads); use Node's own.
// Node hands out its own classes through Response.formData().
const nodeBuffer = (await import(/* @vite-ignore */ ['node', 'buffer'].join(':'))) as {
  File: typeof File
  Blob: typeof Blob
}
vi.stubGlobal('File', nodeBuffer.File)
vi.stubGlobal('Blob', nodeBuffer.Blob)
// ...and its FormData (Node hands one out through Response.formData()).
vi.stubGlobal(
  'FormData',
  (await new Response(new URLSearchParams()).formData()).constructor as typeof FormData,
)

export const MANAGER_PERMISSIONS = [
  'money.read',
  'money.post',
  'money.manage',
  'payments.record',
  'collections.manage',
  'statements.read',
  'reports.read',
  'reports.debtors',
]

export function moneyMe(permissions: string[] = MANAGER_PERMISSIONS): Me {
  const org = meFixture.organizations[0]!
  return {
    ...meFixture,
    organizations: [{ ...org, permissions: permissions.map((permission) => ({ permission })) }],
  }
}

/** Mounts the real app router at a money route with a money-capable session and a fresh model. */
export function mountMoney(path: string, permissions?: string[], today = '2026-11-07') {
  localStorage.clear()
  localStorage.setItem('ob.locale', 'en')
  i18n.activate('en')
  const db = resetMoneyDb(today)
  server.use(http.get('*/v1/me', () => HttpResponse.json(moneyMe(permissions))))
  const queryClient = createQueryClient()
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  const utils = render(
    <Providers queryClient={queryClient} locale="en">
      <RouterProvider router={router} />
    </Providers>,
  )
  return { db, router, queryClient, ...utils }
}

/** Records every request's Idempotency-Key header per "METHOD path" for retry assertions. */
export function recordKeys() {
  const seen: { key: string | null; url: string; method: string }[] = []
  server.events.on('request:start', ({ request }) => {
    seen.push({
      key: request.headers.get('Idempotency-Key'),
      url: request.url,
      method: request.method,
    })
  })
  return seen
}
