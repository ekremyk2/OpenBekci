import { useMemo } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { DownloadIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, ifMatch } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { TableSkeleton } from '@/components/data/Skeletons'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { formatPlate } from './labels'
import { unitQueries } from './queries'

/** Plates registered to a unit (D6) with the camera-operator CSV export. */
export function VehiclesPanel({
  orgId,
  siteId,
  unitId,
  canEdit,
  canExport,
}: {
  orgId: string
  siteId: string
  unitId: string
  canEdit: boolean
  canExport: boolean
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const query = useQuery(unitQueries.vehicles(orgId, siteId, unitId))
  const schema = useMemo(
    () =>
      z.object({
        plate: z
          .string()
          .trim()
          .min(1, t`Enter the plate, for example 34 ABC 123`),
        description: z.string().trim(),
      }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { plate: '', description: '' },
  })
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['tenancy.listVehicles', orgId] })
  const add = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/vehicles', {
          params: { path: { org_id: orgId } },
          body: {
            site_id: siteId,
            unit_id: unitId,
            plate: v.plate,
            description: v.description || null,
          },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      idem.renew()
      form.reset()
      await invalidate()
    },
    onError: (e) => applyError(form, e),
  })
  const archive = useMutation({
    mutationFn: (v: { id: string; version: number; archived: boolean }) =>
      draftApi.PATCH('/v1/orgs/{org_id}/vehicles/{vehicle_id}', {
        params: { path: { org_id: orgId, vehicle_id: v.id }, header: ifMatch(v.version) },
        body: { archived: v.archived },
      }),
    onSuccess: invalidate,
  })
  const remove = useMutation({
    mutationFn: (id: string) =>
      draftApi.DELETE('/v1/orgs/{org_id}/vehicles/{vehicle_id}', {
        params: {
          path: { org_id: orgId, vehicle_id: id },
          header: ifMatch(versions.get(id) ?? '*'),
        },
      }),
    onSuccess: invalidate,
  })
  const versions = new Map((query.data?.items ?? []).map((v) => [v.id, v.version]))
  const { errors } = form.formState
  return (
    <section className="grid gap-3" aria-labelledby="veh-h">
      <div className="flex items-center justify-between gap-2">
        <h2 id="veh-h" className="text-base font-semibold">
          <Trans>Vehicles</Trans>
        </h2>
        {canExport ? (
          <a
            className={buttonVariants({ variant: 'outline', size: 'sm' })}
            href={`/v1/orgs/${orgId}/vehicles:export?site_id=${siteId}&format=csv`}
            download
          >
            <DownloadIcon aria-hidden="true" />
            <Trans>Export plates (CSV)</Trans>
          </a>
        ) : null}
      </div>
      {query.isLoading ? <TableSkeleton rows={2} columns={3} /> : null}
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {query.data && query.data.items.length === 0 ? (
        <EmptyState
          title={<Trans>No vehicles registered</Trans>}
          description={<Trans>Registered plates open the gate for this unit.</Trans>}
        />
      ) : null}
      {query.data && query.data.items.length > 0 ? (
        <ul className="divide-y rounded-md border">
          {query.data.items.map((v) => (
            <li
              key={v.id}
              className="flex flex-wrap items-center justify-between gap-2 p-3 text-sm"
            >
              <div className="flex flex-col">
                <span className="font-mono font-medium" dir="ltr">
                  {formatPlate(v.plate_normalized)}
                </span>
                <span className="text-xs text-muted-foreground">
                  {v.description ?? ''}{' '}
                  {v.plate !== formatPlate(v.plate_normalized) ? `(${v.plate})` : ''}
                </span>
                {v.warning ? <span className="text-xs text-warning">{v.warning}</span> : null}
              </div>
              <div className="flex items-center gap-2">
                {v.archived ? (
                  <Badge variant="secondary">
                    <Trans>Archived</Trans>
                  </Badge>
                ) : null}
                {canEdit ? (
                  <>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() =>
                        archive.mutate({ id: v.id, version: v.version, archived: !v.archived })
                      }
                    >
                      {v.archived ? <Trans>Restore</Trans> : <Trans>Archive</Trans>}
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => remove.mutate(v.id)}>
                      <Trans>Delete</Trans>
                    </Button>
                  </>
                ) : null}
              </div>
            </li>
          ))}
        </ul>
      ) : null}
      {canEdit ? (
        <form
          noValidate
          className="flex flex-wrap items-start gap-2"
          onSubmit={form.handleSubmit((v) => add.mutateAsync(v).catch(() => undefined))}
        >
          <Field id="veh-plate" label={<Trans>Plate</Trans>} error={errors.plate?.message}>
            <Input
              id="veh-plate"
              dir="ltr"
              autoComplete="off"
              aria-invalid={!!errors.plate}
              {...form.register('plate')}
            />
          </Field>
          <Field
            id="veh-desc"
            label={<Trans>Description</Trans>}
            error={errors.description?.message}
          >
            <Input id="veh-desc" {...form.register('description')} />
          </Field>
          <div className="pt-8">
            <SubmitButton busy={add.isPending}>
              <Trans>Add vehicle</Trans>
            </SubmitButton>
          </div>
          <div className="basis-full">
            <FormError message={errors.root?.message} />
          </div>
        </form>
      ) : null}
    </section>
  )
}
