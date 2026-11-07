import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { PageHeader } from '@/components/layout/PageHeader'
import { SiteForm } from '@/features/sites/SiteForm'

export const Route = createFileRoute('/orgs/$orgId/sites/new')({
  component: function NewSite() {
    const { org, can } = useSession()
    const { t } = useLingui()
    const navigate = useNavigate()
    if (!org) return null
    if (!can('sites.manage')) return <ForbiddenPage />
    return (
      <div className="flex flex-col gap-4">
        <PageHeader
          title={<Trans>New site</Trans>}
          crumbs={[{ label: t`Sites`, to: '/orgs/$orgId/sites' }, { label: t`New site` }]}
        />
        <SiteForm
          orgId={org.id}
          onSaved={(site) =>
            void navigate({
              to: '/orgs/$orgId/sites/$siteId',
              params: { orgId: org.id, siteId: site.id },
              search: {},
            })
          }
        />
      </div>
    )
  },
})
