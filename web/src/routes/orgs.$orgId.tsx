import { createFileRoute, Outlet } from '@tanstack/react-router'
import { requireOrg } from '@/app/guards'
import { AppShell } from '@/components/layout/AppShell'

export const Route = createFileRoute('/orgs/$orgId')({
  beforeLoad: async ({ context, location, params }) => requireOrg(context, location, params.orgId),
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
})
