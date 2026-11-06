import { createRouter, type ErrorComponentProps } from '@tanstack/react-router'
import type { QueryClient } from '@tanstack/react-query'
import { AppError } from '@/api/errors'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { ForbiddenPage, NotFoundPage } from '@/components/data/StatusPages'
import { routeTree } from '../routeTree.gen'
import { ForbiddenError } from './guards'

function RouteError({ error, reset }: ErrorComponentProps) {
  if (error instanceof ForbiddenError || (error instanceof AppError && error.isForbidden)) {
    return <ForbiddenPage />
  }
  return (
    <div className="mx-auto max-w-lg p-6">
      <ErrorState error={error} onRetry={reset} />
    </div>
  )
}

export function createAppRouter(queryClient: QueryClient) {
  return createRouter({
    routeTree,
    context: { queryClient },
    defaultPreload: 'intent',
    defaultPreloadStaleTime: 0, // TanStack Query owns caching
    defaultNotFoundComponent: NotFoundPage,
    defaultErrorComponent: RouteError,
    defaultPendingComponent: () => (
      <div className="p-6">
        <PageSkeleton />
      </div>
    ),
    scrollRestoration: true,
  })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
