import { Outlet } from '@tanstack/react-router'
import { useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { AccessDenied } from '@/components/data/AccessDenied'
import { EmptyState } from '@/components/data/EmptyState'
import { useSiteCtx } from './kit/api'
import { Select } from './kit/ui'
import { SectionNav } from './kit/widgets'

/** Governance section frame: permission-filtered links, site switcher. */
export function MeetingsLayout() {
  const { t } = useLingui()
  const { can, org } = useSession()
  const ctx = useSiteCtx()
  const manager = can('meetings.manage')
  const readable = can('meetings.read') || manager
  const items = [
    { to: '/orgs/$orgId/meetings', label: t`Assemblies`, exact: true },
    { to: '/orgs/$orgId/meetings/decisions', label: t`Decisions` },
    ...(manager ? [{ to: '/orgs/$orgId/meetings/settings', label: t`Settings` }] : []),
  ]
  if (!readable) {
    return <AccessDenied />
  }
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        {ctx && ctx.sites.length > 1 ? (
          <Select
            aria-label={t`Site`}
            className="w-auto"
            value={ctx.siteId}
            onChange={(e) => ctx.setSiteId(e.target.value)}
          >
            {ctx.sites.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        ) : ctx ? (
          <span className="text-sm text-muted-foreground">{ctx.siteName}</span>
        ) : null}
      </div>
      <SectionNav label={t`Governance sections`} items={items} orgId={org?.id ?? ''} />
      {ctx ? (
        <Outlet />
      ) : (
        <EmptyState
          title={t`No site yet`}
          description={t`Set up a site first; assemblies are held per site.`}
        />
      )}
    </div>
  )
}
