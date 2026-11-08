import { createRootRouteWithContext, Outlet } from '@tanstack/react-router'
import { ErrorEffects } from '@/app/ErrorEffects'
import type { RouterContext } from '@/app/guards'
import { NotFoundPage } from '@/components/data/StatusPages'

export const Route = createRootRouteWithContext<RouterContext>()({
  component: () => (
    <>
      <Outlet />
      <ErrorEffects />
    </>
  ),
  notFoundComponent: NotFoundPage,
})
