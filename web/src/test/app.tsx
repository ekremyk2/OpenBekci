import { i18n } from '@lingui/core'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { render } from '@testing-library/react'
import { Providers, createQueryClient } from '@/app/providers'
import { createAppRouter } from '@/app/router'
import { server } from './setup'
import { db, meByRoleHandler, type MockRole } from './handlers/operations'

/** Mounts the whole app on `path`, signed in as `role` against the in-memory operations model. */
export function mountApp(path: string, role: MockRole = 'manager') {
  localStorage.clear()
  localStorage.setItem('ob.locale', 'en')
  i18n.activate('en')
  db.role = role
  server.use(meByRoleHandler)
  const queryClient = createQueryClient()
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  render(
    <Providers queryClient={queryClient} locale="en">
      <RouterProvider router={router} />
    </Providers>,
  )
  return { router, queryClient }
}
