import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { PortfolioPage } from '@/features/portfolio/PortfolioPage'

export const Route = createFileRoute('/orgs/$orgId/portfolio')({
  component: function PortfolioRoute() {
    const { can } = useSession()
    if (!can('dashboard.portfolio')) return <ForbiddenPage />
    return <PortfolioPage />
  },
})
