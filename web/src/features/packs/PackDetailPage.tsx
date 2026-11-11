import { useState } from 'react'
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { ArrowLeftIcon, PackageIcon } from 'lucide-react'
import { draftApi } from '@/api/draft'
import { useSession } from '@/app/session'
import { ConfirmDialog } from '@/components/data/ConfirmDialog'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { SchemaForm } from '@/components/schema-form/SchemaForm'
import type { Values } from '@/components/schema-form/schema'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { Label } from '@/components/ui/label'
import { formatInstant } from '@/features/integrations/format'
import { siteQueries } from '@/features/sites/queries'
import { usePayloadKeys, withIdempotencyKey } from '@/lib/idempotency'
import { must } from '@/lib/pagination'
import { toast } from '@/lib/toast'
import {
  packDefaultEntries,
  PackDefaultBadge,
  PackValue,
  useSettingLabel,
} from './PackDefaultBadge'
import { countryName } from './PacksPage'
import { packQueries, schemaOf, usePackLabels, type Pack } from './queries'

/** One pack: enable with its settings, edit org and site settings, see preset defaults, disable. */
export function PackDetailPage({ packId }: { packId: string }) {
  const { org, can } = useSession()
  const { i18n } = useLingui()
  const orgId = org?.id ?? ''
  const label = usePackLabels()
  const query = useQuery({ ...packQueries.list(orgId), enabled: !!orgId })

  if (query.isPending) return <PageSkeleton />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const pack = query.data.find((p) => p.id === packId)
  if (!pack) {
    return (
      <EmptyState
        icon={PackageIcon}
        title={<Trans>Pack not found</Trans>}
        description={<Trans>This server has no pack with this id.</Trans>}
        action={<BackLink orgId={orgId} />}
      />
    )
  }
  const canManage = can('packs.manage')
  const title = label(`${pack.id}.name`) ?? pack.id
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h2 className="flex flex-wrap items-center gap-2 text-lg font-semibold">
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
        </h2>
        <p className="text-sm text-muted-foreground">
          {pack.countries.length > 0 ? (
            <Trans>
              Countries: {pack.countries.map((c) => countryName(c, i18n.locale)).join(', ')}
            </Trans>
          ) : (
            <Trans>Not tied to a country</Trans>
          )}
          {pack.enabled_at ? (
            <>
              {' · '}
              <Trans>Enabled {formatInstant(pack.enabled_at, i18n.locale)}</Trans>
            </>
          ) : null}
        </p>
        {!canManage ? (
          <p role="status" className="text-sm text-muted-foreground">
            <Trans>You can look at the packs but not change them.</Trans>
          </p>
        ) : null}
      </div>
      {pack.enabled ? (
        <EnabledPack pack={pack} orgId={orgId} canManage={canManage} />
      ) : (
        <EnableCard pack={pack} orgId={orgId} canManage={canManage} />
      )}
    </div>
  )
}

function BackLink({ orgId }: { orgId: string }) {
  return (
    <Link
      to="/orgs/$orgId/settings/packs"
      params={{ orgId }}
      className="inline-flex w-fit items-center gap-1 text-sm text-muted-foreground underline-offset-4 hover:underline"
    >
      <ArrowLeftIcon className="size-4 rtl:rotate-180" aria-hidden="true" />
      <Trans>All packs</Trans>
    </Link>
  )
}

/* ───────────── enable ───────────── */

function EnableCard({ pack, orgId, canManage }: { pack: Pack; orgId: string; canManage: boolean }) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const label = usePackLabels()
  const keyFor = usePayloadKeys()
  const schema = schemaOf(pack, 'org')
  const enable = useMutation({
    gcTime: 0, // the variables can hold secrets typed in the form: keep them out of the cache
    mutationFn: async (settings: Values | undefined) =>
      must(
        await draftApi.POST('/v1/orgs/{org_id}/packs/{pack_id}/enable', {
          params: { path: { org_id: orgId, pack_id: pack.id } },
          body: settings ? { settings } : {},
          ...withIdempotencyKey(keyFor('enable', settings ?? null)),
        }),
      ),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['packs.list', orgId] })
      // presets fill site settings when a pack is enabled
      await qc.invalidateQueries({ queryKey: ['tenancy.getSiteSettings', orgId] })
      toast.info(t`Pack enabled.`)
    },
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Enable this pack</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Enabling checks the settings, fills the default values of your sites that you have not
            changed yourself, and switches on what the pack provides. You can disable it again
            later; its data is kept.
          </Trans>
        </CardDescription>
      </CardHeader>
      <CardContent>
        {schema ? (
          <SchemaForm
            schema={schema}
            idPrefix={`enable-${pack.id}`}
            stripPrefix={`${pack.id}_`}
            resolveText={label}
            disabled={!canManage}
            busy={enable.isPending}
            error={enable.error}
            submitLabel={<Trans>Enable pack</Trans>}
            onSubmit={(values) => enable.mutateAsync(values)}
          />
        ) : (
          <div className="flex flex-col gap-3">
            <p className="text-sm text-muted-foreground">
              <Trans>This pack has no settings.</Trans>
            </p>
            <div>
              <Button
                disabled={!canManage || enable.isPending}
                onClick={() => enable.mutate(undefined)}
              >
                <Trans>Enable pack</Trans>
              </Button>
            </div>
            {enable.isError ? (
              <p role="alert" className="text-sm text-destructive">
                <Trans>Something went wrong. Please try again.</Trans>
              </p>
            ) : null}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

/* ───────────── enabled ───────────── */

function EnabledPack({
  pack,
  orgId,
  canManage,
}: {
  pack: Pack
  orgId: string
  canManage: boolean
}) {
  const [confirmDisable, setConfirmDisable] = useState(false)
  const qc = useQueryClient()
  const { t } = useLingui()
  const orgSchema = schemaOf(pack, 'org')
  const siteSchema = schemaOf(pack, 'site')
  const disable = useMutation({
    mutationFn: async () =>
      must(
        await draftApi.POST('/v1/orgs/{org_id}/packs/{pack_id}/disable', {
          params: { path: { org_id: orgId, pack_id: pack.id } },
          ...withIdempotencyKey(crypto.randomUUID()),
        }),
      ),
    onSuccess: async () => {
      setConfirmDisable(false)
      await qc.invalidateQueries({ queryKey: ['packs.list', orgId] })
      toast.info(t`Pack disabled.`)
    },
  })
  return (
    <>
      {orgSchema ? <OrgSettingsCard pack={pack} orgId={orgId} canManage={canManage} /> : null}
      {siteSchema ? <SiteSettingsCard pack={pack} orgId={orgId} canManage={canManage} /> : null}
      <PackDefaultsCard pack={pack} orgId={orgId} />
      {canManage ? (
        <Card>
          <CardHeader>
            <CardTitle>
              <Trans>Disable this pack</Trans>
            </CardTitle>
            <CardDescription>
              <Trans>
                Its providers and importers stop being offered. Its data and settings are kept, and
                the values its defaults filled stay as your sites&apos; own.
              </Trans>
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button variant="destructive" onClick={() => setConfirmDisable(true)}>
              <Trans>Disable pack</Trans>
            </Button>
          </CardContent>
        </Card>
      ) : null}
      <ConfirmDialog
        open={confirmDisable}
        onOpenChange={setConfirmDisable}
        destructive
        busy={disable.isPending}
        title={<Trans>Disable {pack.id}?</Trans>}
        description={
          <Trans>
            Everything this pack provides stops working for your organization until you enable it
            again. Your settings are kept.
          </Trans>
        }
        confirmLabel={<Trans>Disable pack</Trans>}
        onConfirm={() => disable.mutate()}
      >
        {disable.isError ? (
          <p role="alert" className="text-sm text-destructive">
            <Trans>Something went wrong. Please try again.</Trans>
          </p>
        ) : null}
      </ConfirmDialog>
    </>
  )
}

function useSaveSettings(pack: Pack, orgId: string) {
  const qc = useQueryClient()
  const { t } = useLingui()
  const keyFor = usePayloadKeys()
  return useMutation({
    gcTime: 0, // the variables hold secrets typed in the form: keep them out of the cache
    mutationFn: async (input: { scope: 'org' | 'site'; siteId?: string; values: Values }) => {
      const body = {
        scope: input.scope,
        ...(input.siteId ? { site_id: input.siteId } : {}),
        values: input.values,
      }
      return must(
        await draftApi.PUT('/v1/orgs/{org_id}/packs/{pack_id}/settings', {
          params: { path: { org_id: orgId, pack_id: pack.id } },
          body,
          ...withIdempotencyKey(keyFor(`save-${input.scope}-${input.siteId ?? ''}`, body)),
        }),
      )
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['packs.list', orgId] })
      toast.info(t`Settings saved.`)
    },
  })
}

function OrgSettingsCard({
  pack,
  orgId,
  canManage,
}: {
  pack: Pack
  orgId: string
  canManage: boolean
}) {
  const label = usePackLabels()
  const save = useSaveSettings(pack, orgId)
  const [version, setVersion] = useState(0)
  const schema = schemaOf(pack, 'org')!
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Organization settings</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>These apply to the whole organization.</Trans>
        </CardDescription>
      </CardHeader>
      <CardContent>
        <SchemaForm
          key={`${version}-${pack.secrets_set.join(',')}`}
          schema={schema}
          values={pack.settings as Values}
          secretsSet={pack.secrets_set}
          idPrefix={`org-${pack.id}`}
          stripPrefix={`${pack.id}_`}
          resolveText={label}
          disabled={!canManage}
          busy={save.isPending}
          error={save.error}
          submitLabel={<Trans>Save settings</Trans>}
          onSubmit={async (values) => {
            await save.mutateAsync({ scope: 'org', values })
            save.reset() // drops the submitted variables, secrets included
            setVersion((n) => n + 1)
          }}
        />
      </CardContent>
    </Card>
  )
}

function SiteSettingsCard({
  pack,
  orgId,
  canManage,
}: {
  pack: Pack
  orgId: string
  canManage: boolean
}) {
  const { org } = useSession()
  const label = usePackLabels()
  const save = useSaveSettings(pack, orgId)
  const sites = org?.sites ?? []
  const [siteId, setSiteId] = useState(sites[0]?.id ?? '')
  const [version, setVersion] = useState(0)
  const schema = schemaOf(pack, 'site')!
  const stored = pack.sites.find((s) => s.site_id === siteId)
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Site settings</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>These apply to one site, for example a provider account per site.</Trans>
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {sites.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            <Trans>The organization has no sites yet.</Trans>
          </p>
        ) : (
          <>
            <div className="flex max-w-xs flex-col gap-2">
              <Label htmlFor={`site-pick-${pack.id}`}>
                <Trans>Site</Trans>
              </Label>
              <Select
                id={`site-pick-${pack.id}`}
                value={siteId}
                onChange={(e) => setSiteId(e.target.value)}
              >
                {sites.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </div>
            <SchemaForm
              key={`${siteId}-${version}-${stored?.secrets_set.join(',') ?? ''}`}
              schema={schema}
              values={(stored?.values ?? {}) as Values}
              secretsSet={stored?.secrets_set ?? []}
              idPrefix={`site-${pack.id}`}
              stripPrefix={`${pack.id}_`}
              resolveText={label}
              disabled={!canManage}
              busy={save.isPending}
              error={save.error}
              submitLabel={<Trans>Save site settings</Trans>}
              onSubmit={async (values) => {
                await save.mutateAsync({ scope: 'site', siteId, values })
                save.reset()
                setVersion((n) => n + 1)
              }}
            />
          </>
        )}
      </CardContent>
    </Card>
  )
}

/** The core site settings this pack's presets filled and the sites have not changed since. */
function PackDefaultsCard({ pack, orgId }: { pack: Pack; orgId: string }) {
  const { org, can } = useSession()
  const sites = org?.sites ?? []
  const canRead = can('sites.read')
  const label = useSettingLabel()
  const [raw, setRaw] = useState(false)
  const results = useQueries({
    queries: sites.map((s) => ({ ...siteQueries.settings(orgId, s.id), enabled: canRead })),
  })
  const rows = sites.flatMap((s, i) =>
    packDefaultEntries(results[i]?.data, pack.id).map(([key, entry]) => ({
      site: s.name,
      siteId: s.id,
      key,
      value: entry.value,
    })),
  )
  if (!canRead || sites.length === 0) return null
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Pack defaults on your sites</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Values this pack filled into the site settings. Change one on the site and it becomes
            the site&apos;s own.
          </Trans>
        </CardDescription>
      </CardHeader>
      <CardContent>
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            <Trans>This pack has not filled in any site setting, or the sites changed them.</Trans>
          </p>
        ) : (
          <ul className="divide-y rounded-md border text-sm">
            {rows.map((r) => (
              <li
                key={`${r.siteId}-${r.key}`}
                className="flex flex-wrap items-center justify-between gap-2 p-3"
              >
                <span className="flex flex-col">
                  <span className="font-medium">{label(r.key)}</span>
                  <span className="text-xs text-muted-foreground">{r.site}</span>
                </span>
                <span className="flex min-w-0 flex-col items-start gap-1">
                  <PackDefaultBadge pack={pack.id} />
                  <PackValue value={r.value} raw={raw} />
                </span>
              </li>
            ))}
          </ul>
        )}
        {rows.length > 0 && can('sites.manage') ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="mt-2"
            aria-pressed={raw}
            onClick={() => setRaw((r) => !r)}
          >
            <Trans>Raw</Trans>
          </Button>
        ) : null}
      </CardContent>
    </Card>
  )
}
