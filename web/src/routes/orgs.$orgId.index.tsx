import { createFileRoute, Navigate } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { LayoutDashboardIcon } from 'lucide-react'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { SiteDashboardView } from '@/features/dashboard/SiteDashboardView'
import { StaffDashboardView } from '@/features/dashboard/StaffDashboardView'

export const Route = createFileRoute('/orgs/$orgId/')({
  component: function Dashboard() {
    const { org, can } = useSession()
    if (can('dashboard.site')) return <SiteDashboardView />
    if (can('dashboard.staff')) return <StaffDashboardView />
    // the gate guard has no dashboard: land on the console
    if (can('gate.operate') && org)
      return <Navigate to="/orgs/$orgId/visitors/gate" params={{ orgId: org.id }} replace />
    return (
      <div className="flex flex-col gap-6">
        <h1 className="text-2xl font-semibold">{org?.name}</h1>
        <EmptyState
          icon={LayoutDashboardIcon}
          title={<Trans>Your dashboard is on its way</Trans>}
          description={<Trans>Balances, open requests and announcements will appear here.</Trans>}
        />
      </div>
    )
  },
})
