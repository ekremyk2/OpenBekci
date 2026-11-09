import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { PackageIcon } from 'lucide-react'
import { draftApi } from '@/api/draft'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { Badge } from '@/components/ui/badge'
import { buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { must } from '@/lib/pagination'
import { formatInstant } from '@/features/integrations/format'
import { packQueries, usePackLabels, type Pack } from './queries'

/** Names of ISO country codes in the UI language; the code itself when the browser lacks it. */
export function countryName(code: string, locale: string): string {
  try {
    return new Intl.DisplayNames([locale], { type: 'region' }).of(code) ?? code
  } catch {
    return code
  }
}

/** The packs compiled into the server, with this organization's state (web plan §9.4). */
export function PacksPage() {
  const { org, can } = useSession()
  const { t, i18n } = useLingui()
  const orgId = org?.id ?? ''
  const label = usePackLabels()
  const query = useQuery({ ...packQueries.list(orgId), enabled: !!orgId })
  const orgQuery = useQuery({
    queryKey: ['tenancy.getOrg', orgId],
    enabled: !!orgId && can('org.read'),
    queryFn: async () =>
      must(await draftApi.GET('/v1/orgs/{org_id}', { params: { path: { org_id: orgId } } })),
  })

  if (query.isPending) return <PageSkeleton />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const packs = query.data
  const country = orgQuery.data?.country_code

  return (
    <section className="flex flex-col gap-4" aria-labelledby="packs-title">
      <div className="flex flex-col gap-1">
        <h2 id="packs-title" className="text-lg font-semibold">
          <Trans>Packs</Trans>
        </h2>
        <p className="max-w-prose text-sm text-muted-foreground">
          <Trans>
            A pack adds the rules, providers and texts of a country to OpenBekci. Enable the ones
            you need and fill in their settings.
          </Trans>
        </p>
      </div>
      {packs.length === 0 ? (
        <EmptyState
          icon={PackageIcon}
          title={<Trans>No packs on this server</Trans>}
          description={<Trans>Packs are compiled into the server by its operator.</Trans>}
        />
      ) : (
        <ul className="grid gap-4 md:grid-cols-2" aria-label={t`Packs`}>
          {packs.map((p) => (
            <PackCard
              key={p.id}
              pack={p}
              locale={i18n.locale}
              title={label(`${p.id}.name`) ?? p.id}
              suggested={!p.enabled && !!country && p.countries.includes(country)}
              canManage={can('packs.manage')}
              orgId={orgId}
            />
          ))}
        </ul>
      )}
    </section>
  )
}

function PackCard({
  pack,
  title,
  suggested,
  canManage,
  orgId,
  locale,
}: {
  pack: Pack
  title: string
  suggested: boolean
  canManage: boolean
  orgId: string
  locale: string
}) {
  const { t } = useLingui()
  const hasSettings = Object.keys(pack.settings_schemas as object).length > 0
  return (
    <li>
      <Card className="h-full">
        <CardHeader>
          <CardTitle className="flex flex-wrap items-center gap-2">
            {title}
            <Badge variant="outline">v{pack.version}</Badge>
            {pack.enabled ? (
              <Badge variant="success">
                <Trans>Enabled</Trans>
              </Badge>
            ) : (
              <Badge variant="secondary">
                <Trans>Not enabled</Trans>
              </Badge>
            )}
            {suggested ? (
              <Badge variant="info">
                <Trans>Suggested for your country</Trans>
              </Badge>
            ) : null}
          </CardTitle>
          <CardDescription>
            {pack.countries.length > 0 ? (
              <Trans>
                Countries: {pack.countries.map((c) => countryName(c, locale)).join(', ')}
              </Trans>
            ) : (
              <Trans>Not tied to a country</Trans>
            )}
            {pack.enabled_at ? (
              <>
                {' · '}
                <Trans>Enabled {formatInstant(pack.enabled_at, locale)}</Trans>
              </>
            ) : null}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {pack.provides.length > 0 ? (
            <ul className="flex flex-wrap gap-1" aria-label={t`Provides`}>
              {pack.provides.map((x) => (
                <li key={x}>
                  <Badge variant="secondary" className="font-mono">
                    {x}
                  </Badge>
                </li>
              ))}
            </ul>
          ) : null}
          <div>
            <Link
              to="/orgs/$orgId/settings/packs/$packId"
              params={{ orgId, packId: pack.id }}
              className={buttonVariants({
                variant: pack.enabled ? 'outline' : 'default',
                size: 'sm',
              })}
            >
              {pack.enabled ? (
                <Trans>Settings</Trans>
              ) : canManage ? (
                hasSettings ? (
                  <Trans>Set up and enable</Trans>
                ) : (
                  <Trans>Enable</Trans>
                )
              ) : (
                <Trans>Details</Trans>
              )}
              <span className="sr-only"> {title}</span>
            </Link>
          </div>
        </CardContent>
      </Card>
    </li>
  )
}
