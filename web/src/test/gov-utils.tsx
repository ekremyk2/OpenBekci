import { i18n } from '@lingui/core'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { render } from '@testing-library/react'
import { vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import type { Me } from '@/api/schema'
import { Providers, createQueryClient } from '@/app/providers'
import { createAppRouter } from '@/app/router'
import { meFixture } from '@/test/handlers'
import { resetAccessDb } from '@/test/handlers/access'
import { resetCommunityDb } from '@/test/handlers/community'
import { resetGovernanceDb } from '@/test/handlers/governance'
import { resetCounter } from '@/test/handlers/gov-util'
import { server } from '@/test/setup'

// jsdom's File/Blob cannot be serialised by Node's fetch (multipart uploads); use Node's own.
const nodeBuffer = (await import(/* @vite-ignore */ ['node', 'buffer'].join(':'))) as {
  File: typeof File
  Blob: typeof Blob
}
vi.stubGlobal('File', nodeBuffer.File)
vi.stubGlobal('Blob', nodeBuffer.Blob)

export const MANAGER = [
  'meetings.read',
  'meetings.manage',
  'decisions.manage',
  'attendance.manage',
  'facilities.manage',
  'bookings.read',
  'bookings.create',
  'bookings.manage',
  'visitors.read',
  'visitors.manage',
  'visitors.decide',
  'gate.operate',
  'access.read',
  'access.manage',
  'access.open',
  'access.view_events',
]

export function govMe(permissions: string[] = MANAGER): Me {
  const org = meFixture.organizations[0]!
  return {
    ...meFixture,
    organizations: [{ ...org, permissions: permissions.map((permission) => ({ permission })) }],
    occupancies: [
      {
        id: 'occ-1',
        organization_id: org.id,
        site_id: 'site-a',
        unit_id: 'u-a1',
        unit_label: 'A1',
        kind: 'owner',
        may_see_money: true,
      },
    ],
  }
}

/** Mounts the real app router at a route with a session holding `permissions` and fresh models. */
export function mountGov(path: string, permissions?: string[]) {
  localStorage.clear()
  localStorage.setItem('ob.locale', 'en')
  sessionStorage.clear()
  i18n.activate('en')
  resetCounter()
  resetGovernanceDb()
  resetCommunityDb()
  resetAccessDb()
  server.use(http.get('*/v1/me', () => HttpResponse.json(govMe(permissions))))
  const queryClient = createQueryClient()
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  const utils = render(
    <Providers queryClient={queryClient} locale="en">
      <RouterProvider router={router} />
    </Providers>,
  )
  return { router, queryClient, ...utils }
}

/** Records the headers of every request for header assertions. */
export function recordRequests() {
  const seen: { method: string; url: string; headers: Headers; body?: string }[] = []
  server.events.on('request:start', ({ request }) => {
    seen.push({ method: request.method, url: request.url, headers: new Headers(request.headers) })
  })
  return seen
}
