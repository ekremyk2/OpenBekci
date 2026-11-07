import { configure, render, waitFor } from '@testing-library/react'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { i18n } from '@lingui/core'
import { Providers, createQueryClient } from '@/app/providers'
import { createAppRouter } from '@/app/router'
import { jobPolling } from '@/features/import/useJob'
import { server } from './setup'
import { resetDb } from './handlers/db'
import { adminHandlers } from './handlers/me'

/*
 * jsdom's FormData/Blob lose their bytes inside Node's fetch. Upload tests build multipart bodies
 * with Node's FormData and Node's File (`nodeFile`) instead.
 */
const nodeBuffer = await import('node:buffer')
globalThis.FormData = (
  await new Response('a=1', {
    headers: { 'content-type': 'application/x-www-form-urlencoded' },
  }).formData()
).constructor as typeof FormData

// the real router, MSW and several providers make single findBy* calls slow on a busy CI machine
configure({ asyncUtilTimeout: 4000 })

/** A File whose bytes survive the trip through fetch in tests. */
export function nodeFile(parts: string[], name: string, type = 'text/csv'): File {
  return new nodeBuffer.File(parts, name, { type }) as unknown as File
}

/**
 * Mounts the real router (guards, shell, route components) at `path` against the in-memory draft
 * backend, signed in as an admin with `permissions` (default: all of them).
 */
export async function mountApp(
  path: string,
  opts: { permissions?: string[]; seed?: (db: ReturnType<typeof resetDb>) => void } = {},
) {
  localStorage.clear()
  i18n.activate('en')
  const fresh = resetDb()
  opts.seed?.(fresh)
  jobPolling.ms = 10
  server.use(...adminHandlers(opts.permissions))
  const queryClient = createQueryClient()
  queryClient.setDefaultOptions({ queries: { retry: false, staleTime: 0 } })
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  const utils = render(
    <Providers queryClient={queryClient} locale="en">
      <RouterProvider router={router} />
    </Providers>,
  )
  await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5000 })
  return { router, queryClient, ...utils }
}
