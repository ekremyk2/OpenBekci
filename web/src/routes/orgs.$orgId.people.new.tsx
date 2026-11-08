import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { PageHeader } from '@/components/layout/PageHeader'
import { PersonForm } from '@/features/people/PersonForm'

export const Route = createFileRoute('/orgs/$orgId/people/new')({
  component: function NewPerson() {
    const { org, can } = useSession()
    const { t } = useLingui()
    const navigate = useNavigate()
    if (!org) return null
    if (!can('people.manage')) return <ForbiddenPage />
    return (
      <div className="flex flex-col gap-4">
        <PageHeader
          title={<Trans>Add a person</Trans>}
          crumbs={[{ label: t`People`, to: '/orgs/$orgId/people' }, { label: t`Add a person` }]}
        />
        <PersonForm
          orgId={org.id}
          onSaved={(p) =>
            void navigate({
              to: '/orgs/$orgId/people/$personId',
              params: { orgId: org.id, personId: p.id },
              search: { tab: 'invitations' },
            })
          }
        />
      </div>
    )
  },
})
