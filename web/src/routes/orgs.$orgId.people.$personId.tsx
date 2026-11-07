import { createFileRoute } from '@tanstack/react-router'
import { PERSON_TABS, PersonDetailPage, type PersonTab } from '@/features/people/PersonDetailPage'

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
