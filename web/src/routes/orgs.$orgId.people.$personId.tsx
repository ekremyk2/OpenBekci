import { createFileRoute } from '@tanstack/react-router'
import { PersonDetailPage } from '@/features/people/PersonDetailPage'
import { PERSON_TABS, type PersonTab } from '@/features/people/tabs'

export const Route = createFileRoute('/orgs/$orgId/people/$personId')({
  validateSearch: (s: Record<string, unknown>): { tab?: PersonTab } => ({
    tab: PERSON_TABS.find((t) => t === s.tab),
  }),
  component: function PersonRoute() {
    return (
      <PersonDetailPage
        personId={Route.useParams().personId}
        tab={Route.useSearch().tab ?? 'profile'}
      />
    )
  },
})
