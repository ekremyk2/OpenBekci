import { Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { PageHeader } from '@/components/layout/PageHeader'
import { Badge } from '@/components/ui/badge'
import { buttonVariants } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { siteQueries } from './queries'
import { SiteForm } from './SiteForm'
import { SiteBankTab, SiteBlocksTab, SiteLockTab, SiteSettingsTab } from './SiteTabs'
import { TransferPanel } from './TransferPanel'

export const SITE_TABS = ['general', 'settings', 'bank', 'lock', 'blocks', 'transfer'] as const
export type SiteTab = (typeof SITE_TABS)[number]

export function SiteDetailPage({ siteId, tab }: { siteId: string; tab: SiteTab }) {
  const { org, can } = useSession()
  const { t } = useLingui()
  const navigate = useNavigate()
  const orgId = org?.id ?? ''
  const query = useQuery({ ...siteQueries.one(orgId, siteId), enabled: !!org })
  if (query.isLoading) return <PageSkeleton />
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const site = query.data!
  const scope = { siteId }
  const canManage = can('sites.manage', scope)
  const canBank = can('sites.bank_details', scope)
  const canExport = can('sites.export')
  const canOffboard = can('sites.offboard')
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            {site.name}
            {site.status !== 'active' ? <Badge variant="secondary">{site.status}</Badge> : null}
          </span>
        }
        crumbs={[{ label: t`Sites`, to: '/orgs/$orgId/sites' }, { label: site.name }]}
        actions={
          <>
            <Link
              to="/orgs/$orgId/units"
              params={{ orgId }}
              search={{ site: siteId }}
              className={buttonVariants({ variant: 'outline' })}
            >
              <Trans>Units</Trans>
            </Link>
            {can('claims.decide') ? (
              <Link
                to="/orgs/$orgId/sites/$siteId/qr"
                params={{ orgId, siteId }}
                className={buttonVariants({ variant: 'outline' })}
              >
                <Trans>Site QR code</Trans>
              </Link>
            ) : null}
          </>
        }
      />
      <Tabs
        value={tab}
        onValueChange={(v) => void navigate({ to: '.', search: { tab: v as SiteTab } as never })}
      >
        <TabsList className="flex-wrap">
          <TabsTrigger value="general">{t`General`}</TabsTrigger>
          <TabsTrigger value="settings">{t`Settings`}</TabsTrigger>
          {canBank ? <TabsTrigger value="bank">{t`Bank details`}</TabsTrigger> : null}
          <TabsTrigger value="lock">{t`Lock date`}</TabsTrigger>
          <TabsTrigger value="blocks">{t`Blocks`}</TabsTrigger>
          {canExport || canOffboard ? (
            <TabsTrigger value="transfer">{t`Export and offboard`}</TabsTrigger>
          ) : null}
        </TabsList>
        <TabsContent value="general" className="pt-4">
          <SiteForm orgId={orgId} site={site} readOnly={!canManage} />
        </TabsContent>
        <TabsContent value="settings" className="pt-4">
          <SiteSettingsTab orgId={orgId} siteId={siteId} canEdit={canManage} />
        </TabsContent>
        {canBank ? (
          <TabsContent value="bank" className="pt-4">
            <SiteBankTab orgId={orgId} siteId={siteId} canEdit={canManage} />
          </TabsContent>
        ) : null}
        <TabsContent value="lock" className="pt-4">
          <SiteLockTab orgId={orgId} siteId={siteId} timezone={site.timezone} />
        </TabsContent>
        <TabsContent value="blocks" className="pt-4">
          <SiteBlocksTab orgId={orgId} siteId={siteId} canEdit={canManage} />
        </TabsContent>
        {canExport || canOffboard ? (
          <TabsContent value="transfer" className="pt-4">
            <TransferPanel
              orgId={orgId}
              site={site}
              canExport={canExport}
              canOffboard={canOffboard}
            />
          </TabsContent>
        ) : null}
      </Tabs>
    </div>
  )
}
