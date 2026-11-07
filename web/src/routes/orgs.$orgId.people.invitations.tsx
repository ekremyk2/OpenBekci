import { createFileRoute } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { PageHeader } from '@/components/layout/PageHeader'
import { InvitationsPanel } from '@/features/people/InvitationsPanel'
import { PeopleNav } from '@/features/people/PeopleNav'

export const Route = createFileRoute('/orgs/$orgId/people/invitations')({
  component: function InvitationsRoute() {
    const { org, can } = useSession()
    if (!org) return null
    if (!can('invitations.manage')) return <ForbiddenPage />
    return (
      <div className="flex flex-col gap-4">
        <PageHeader
          title={<Trans>Invitations</Trans>}
          description={<Trans>Who was invited and who has joined.</Trans>}
        />
        <PeopleNav />
        <InvitationsPanel orgId={org.id} />
      </div>
    )
  },
})
