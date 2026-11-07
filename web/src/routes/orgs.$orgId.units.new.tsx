import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { PageHeader } from '@/components/layout/PageHeader'
import { useActiveSite } from '@/features/sites/useActiveSite'
import { UnitForm } from '@/features/units/UnitForm'

export const Route = createFileRoute('/orgs/$orgId/units/new')({
  validateSearch: (s: Record<string, unknown>): { site?: string } => ({
    site: typeof s.site === 'string' && s.site ? s.site : undefined,
  }),
  component: function NewUnit() {
    const { org, can } = useSession()
    const { t } = useLingui()
    const navigate = useNavigate()
    const { siteId } = useActiveSite(Route.useSearch().site)
    if (!org) return null
    if (!can('units.manage')) return <ForbiddenPage />
    if (!siteId) return null
    return (
      <div className="flex flex-col gap-4">
        <PageHeader
          title={<Trans>New unit</Trans>}
          crumbs={[
            { label: t`Units`, to: '/orgs/$orgId/units', search: { site: siteId } },
            { label: t`New unit` },
          ]}
        />
        <UnitForm
          orgId={org.id}
          siteId={siteId}
          onSaved={(u) =>
            void navigate({
              to: '/orgs/$orgId/units/$unitId',
              params: { orgId: org.id, unitId: u.id },
            })
          }
        />
      </div>
    )
  },
})
