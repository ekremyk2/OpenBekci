import { createFileRoute } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { LayoutDashboardIcon } from 'lucide-react'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'

export const Route = createFileRoute('/orgs/$orgId/')({
  component: function Dashboard() {
    const { org } = useSession()
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
