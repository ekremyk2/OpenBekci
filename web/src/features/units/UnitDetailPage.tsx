import { Link, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, ifMatchOf } from '@/api/draft'
import { useSession } from '@/app/session'
import { SiteDate } from '@/components/data/SiteDate'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { PageHeader } from '@/components/layout/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { occupancyKindLabel } from '@/lib/labels'
import { UnitForm } from './UnitForm'
import { unitTypeLabel } from './labels'
import { unitQueries } from './queries'
import { VehiclesPanel } from './VehiclesPanel'

export function UnitDetailPage({ unitId }: { unitId: string }) {
  const { org, can } = useSession()
  const { t, i18n } = useLingui()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const orgId = org?.id ?? ''
  const unitQ = useQuery({ ...unitQueries.one(orgId, unitId), enabled: !!org })
  const unit = unitQ.data
  const siteId = unit?.site_id ?? ''
  const site = org?.sites.find((s) => s.id === siteId)
  const parentQ = useQuery({
    ...unitQueries.one(orgId, unit?.parent_unit_id ?? ''),
    enabled: !!unit?.parent_unit_id,
  })
  const kidsQ = useQuery({
    ...unitQueries.list(orgId, { siteId, parentUnitId: unitId }, 100),
    enabled: !!unit && !unit.parent_unit_id,
  })
  const occQ = useQuery({
    queryKey: ['people.listOccupancies', orgId, { unit_id: unitId }],
    enabled: !!unit && can('occupancies.read', { siteId }),
    queryFn: async () => {
      const { data } = await draftApi.GET('/v1/orgs/{org_id}/occupancies', {
        params: { path: { org_id: orgId }, query: { unit_id: unitId, limit: 100 } },
      })
      return data!
    },
  })
  const remove = useMutation({
    mutationFn: () =>
      draftApi.DELETE('/v1/orgs/{org_id}/units/{unit_id}', {
        params: { path: { org_id: orgId, unit_id: unitId }, header: ifMatchOf(unit?.version) },
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['tenancy.listUnits', orgId] })
      await navigate({ to: '/orgs/$orgId/units', params: { orgId }, search: { site: siteId } })
    },
  })

  if (unitQ.isLoading) return <PageSkeleton />
  if (unitQ.error || !unit)
    return <ErrorState error={unitQ.error} onRetry={() => void unitQ.refetch()} />
  const scope = { siteId }
  const canManage = can('units.manage', scope)
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            {unit.door_number}
            {unit.parent_unit_id ? (
              <Badge variant="outline">
                <Trans>Appurtenance</Trans>
              </Badge>
            ) : unit.vacant ? (
              <Badge variant="warning">
                <Trans>Vacant</Trans>
              </Badge>
            ) : (
              <Badge variant="success">
                <Trans>Occupied</Trans>
              </Badge>
            )}
          </span>
        }
        description={unitTypeLabel(unit.type, i18n)}
        crumbs={[
          { label: t`Units`, to: '/orgs/$orgId/units', search: { site: siteId } },
          { label: unit.door_number },
        ]}
        actions={
          canManage ? (
            <Button
              variant="destructive"
              disabled={remove.isPending}
              onClick={() => remove.mutate()}
            >
              <Trans>Delete unit</Trans>
            </Button>
          ) : null
        }
      />
      {remove.error ? <ErrorState error={remove.error} /> : null}

      {canManage ? (
        <UnitForm
          orgId={orgId}
          siteId={siteId}
          unit={unit}
          parentLabel={parentQ.data?.door_number}
        />
      ) : null}

      {parentQ.data ? (
        <p className="text-sm">
          <Trans>Belongs to</Trans>{' '}
          <Link
            to="/orgs/$orgId/units/$unitId"
            params={{ orgId, unitId: parentQ.data.id }}
            className="font-medium underline-offset-4 hover:underline"
          >
            {parentQ.data.door_number}
          </Link>
        </p>
      ) : null}

      {!unit.parent_unit_id ? (
        <section className="grid gap-2" aria-labelledby="app-h">
          <h2 id="app-h" className="text-base font-semibold">
            <Trans>Appurtenances</Trans>
          </h2>
          {kidsQ.data && kidsQ.data.items.length > 0 ? (
            <ul className="divide-y rounded-md border text-sm">
              {kidsQ.data.items.map((k) => (
                <li key={k.id} className="flex items-center justify-between p-3">
                  <Link
                    to="/orgs/$orgId/units/$unitId"
                    params={{ orgId, unitId: k.id }}
                    className="font-medium underline-offset-4 hover:underline"
                  >
                    {k.door_number}
                  </Link>
                  <span className="text-muted-foreground">{unitTypeLabel(k.type, i18n)}</span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-sm text-muted-foreground">
              <Trans>No parking or storage belongs to this unit.</Trans>
            </p>
          )}
        </section>
      ) : null}

      {can('occupancies.read', scope) ? (
        <section className="grid gap-2" aria-labelledby="occ-h">
          <h2 id="occ-h" className="text-base font-semibold">
            <Trans>People</Trans>
          </h2>
          {occQ.error ? <ErrorState error={occQ.error} /> : null}
          {occQ.data && occQ.data.items.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              <Trans>Nobody is linked to this unit.</Trans>
            </p>
          ) : null}
          <ul className="divide-y rounded-md border text-sm">
            {(occQ.data?.items ?? []).map((o) => (
              <li key={o.id} className="flex flex-wrap items-center justify-between gap-2 p-3">
                <Link
                  to="/orgs/$orgId/people/$personId"
                  params={{ orgId, personId: o.person_id }}
                  className="font-medium underline-offset-4 hover:underline"
                >
                  {o.person_display_name}
                </Link>
                <span className="flex items-center gap-2 text-muted-foreground">
                  {occupancyKindLabel(o.kind, i18n)}
                  <SiteDate value={o.valid_from} timeZone={site?.timezone ?? 'UTC'} />
                  {o.valid_to ? (
                    <>
                      – <SiteDate value={o.valid_to} timeZone={site?.timezone ?? 'UTC'} />
                    </>
                  ) : null}
                </span>
                {!o.valid_to && can('people.move_out', scope) ? (
                  <Link
                    to="/orgs/$orgId/move-out/$occupancyId"
                    params={{ orgId, occupancyId: o.id }}
                    className="text-sm underline-offset-4 hover:underline"
                  >
                    <Trans>Move out…</Trans>
                  </Link>
                ) : null}
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {can('vehicles.read', scope) ? (
        <VehiclesPanel
          orgId={orgId}
          siteId={siteId}
          unitId={unitId}
          canEdit={can('vehicles.manage', scope)}
          canExport
        />
      ) : null}
    </div>
  )
}
