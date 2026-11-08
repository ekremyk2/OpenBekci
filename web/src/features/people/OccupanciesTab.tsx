import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { occupancyKindLabel } from '@/lib/labels'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { SiteDate } from '@/components/data/SiteDate'
import { todayInZone } from '@/lib/dates'
import { useActiveSite } from '@/features/sites/useActiveSite'
import { PageSkeleton } from '@/components/data/Skeletons'
import { UnitPicker, type PickerOption } from '@/components/forms/EntityPicker'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { Badge } from '@/components/ui/badge'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { sideLabel } from './labels'
import { personQueries } from './queries'

/** Occupancy timeline of one person across units, with the "may see money" toggle (S3). */
export function OccupanciesTab({
  orgId,
  personId,
  canEdit,
}: {
  orgId: string
  personId: string
  canEdit: boolean
}) {
  const { t, i18n } = useLingui()
  const { can } = useSession()
  const queryClient = useQueryClient()
  const query = useQuery(personQueries.occupancies(orgId, personId))
  const { timezone: tz } = useActiveSite()
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['people.listOccupancies', orgId] })
  const money = useMutation({
    mutationFn: (v: { id: string; on: boolean }) =>
      runWithReauth((key) =>
        draftApi.PUT('/v1/orgs/{org_id}/occupancies/{occupancy_id}/money-access', {
          params: { path: { org_id: orgId, occupancy_id: v.id } },
          body: { may_see_money: v.on },
          ...withIdempotencyKey(key),
        }),
      ),
    onSuccess: invalidate,
  })
  if (query.isLoading) return <PageSkeleton />
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const items = [...query.data!.items].sort((a, b) => b.valid_from.localeCompare(a.valid_from))
  return (
    <div className="grid gap-4">
      {items.length === 0 ? (
        <EmptyState
          title={<Trans>Not linked to any unit</Trans>}
          description={
            <Trans>Link this person to a unit as owner, tenant or household member.</Trans>
          }
        />
      ) : (
        <ol aria-label={t`Occupancy timeline`} className="grid gap-2">
          {items.map((o) => {
            const active = !o.valid_to
            return (
              <li
                key={o.id}
                className="flex flex-wrap items-center gap-3 rounded-md border p-3 text-sm"
              >
                <Link
                  to="/orgs/$orgId/units/$unitId"
                  params={{ orgId, unitId: o.unit_id }}
                  className="font-medium underline-offset-4 hover:underline"
                >
                  {o.unit_door_number}
                </Link>
                <Badge variant="secondary">{occupancyKindLabel(o.kind, i18n)}</Badge>
                <span className="text-muted-foreground">{sideLabel(o.side, i18n)}</span>
                {o.share_pct != null ? <span className="tabular-nums">{o.share_pct}%</span> : null}
                {o.is_correspondent ? (
                  <Badge variant="info">
                    <Trans>Correspondent</Trans>
                  </Badge>
                ) : null}
                <span className="text-muted-foreground">
                  <SiteDate value={o.valid_from} timeZone={tz} />
                  {' – '}
                  {o.valid_to ? (
                    <SiteDate value={o.valid_to} timeZone={tz} />
                  ) : (
                    <Trans>today</Trans>
                  )}
                </span>
                <label className="ms-auto flex items-center gap-2">
                  <Checkbox
                    checked={o.may_see_money}
                    disabled={!can('occupancies.grant_money') || !active || money.isPending}
                    onChange={(e) => money.mutate({ id: o.id, on: e.target.checked })}
                  />
                  <Trans>May see money</Trans>
                </label>
                {active && can('people.move_out') ? (
                  <Link
                    to="/orgs/$orgId/move-out/$occupancyId"
                    params={{ orgId, occupancyId: o.id }}
                    className="underline-offset-4 hover:underline"
                  >
                    <Trans>Move out…</Trans>
                  </Link>
                ) : null}
              </li>
            )
          })}
        </ol>
      )}
      {canEdit ? <AddOccupancy orgId={orgId} personId={personId} onAdded={invalidate} /> : null}
    </div>
  )
}

function AddOccupancy({
  orgId,
  personId,
  onAdded,
}: {
  orgId: string
  personId: string
  onAdded: () => void
}) {
  const { t } = useLingui()
  const { org } = useSession()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const sites = org?.sites ?? []
  const active = useActiveSite()
  const [siteId, setSiteId] = useState(active.siteId ?? sites[0]?.id ?? '')
  const [unit, setUnit] = useState<PickerOption | null>(null)
  const schema = useMemo(
    () =>
      z.object({
        kind: z.string(),
        share_pct: z
          .string()
          .refine(
            (v) => v === '' || (Number(v) > 0 && Number(v) <= 100),
            t`Enter a share between 1 and 100`,
          ),
        is_correspondent: z.boolean(),
        valid_from: z.string().min(1, t`Choose the start date`),
        valid_to: z.string(),
      }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: {
      kind: 'owner',
      share_pct: '',
      is_correspondent: false,
      valid_from: todayInZone(active.timezone),
      valid_to: '',
    },
  })
  const kind = useWatch({ control: form.control, name: 'kind' })
  const add = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/occupancies', {
          params: { path: { org_id: orgId } },
          body: {
            person_id: personId,
            unit_id: unit!.id,
            kind: v.kind as 'owner',
            side: v.kind === 'tenant' ? 'tenant' : 'owner',
            share_pct: v.kind === 'owner' && v.share_pct ? Number(v.share_pct) : null,
            is_correspondent: v.is_correspondent,
            valid_from: v.valid_from,
            valid_to: v.valid_to || null,
          },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: () => {
      idem.renew()
      form.reset()
      setUnit(null)
      onAdded()
    },
    onError: (e) => applyError(form, e),
  })
  const [unitError, setUnitError] = useState<string | undefined>()
  const { errors } = form.formState
  return (
    <form
      noValidate
      aria-label={t`Link to a unit`}
      className="grid max-w-2xl gap-3 rounded-md border p-4 sm:grid-cols-2"
      onSubmit={form.handleSubmit((v) => {
        if (!unit) return setUnitError(t`Choose a unit`)
        setUnitError(undefined)
        return add.mutateAsync(v).catch(() => undefined)
      })}
    >
      <h3 className="text-sm font-semibold sm:col-span-2">
        <Trans>Link to a unit</Trans>
      </h3>
      {sites.length > 1 ? (
        <Field id="occ-site" label={<Trans>Site</Trans>}>
          <Select
            id="occ-site"
            value={siteId}
            onChange={(e) => {
              setSiteId(e.target.value)
              setUnit(null)
            }}
          >
            {sites.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        </Field>
      ) : null}
      <Field id="occ-unit" label={<Trans>Unit</Trans>} error={unitError}>
        <UnitPicker
          id="occ-unit"
          orgId={orgId}
          siteId={siteId}
          value={unit}
          onChange={setUnit}
          invalid={!!unitError}
        />
      </Field>
      <Field id="occ-kind" label={<Trans>Relationship</Trans>}>
        <Select id="occ-kind" {...form.register('kind')}>
          <option value="owner">{t`Owner`}</option>
          <option value="tenant">{t`Tenant`}</option>
          <option value="household">{t`Household member`}</option>
        </Select>
      </Field>
      {kind === 'owner' ? (
        <Field
          id="occ-share"
          label={<Trans>Ownership share (%)</Trans>}
          error={errors.share_pct?.message}
        >
          <Input
            id="occ-share"
            inputMode="decimal"
            aria-invalid={!!errors.share_pct}
            {...form.register('share_pct')}
          />
        </Field>
      ) : null}
      <Field id="occ-from" label={<Trans>From</Trans>} error={errors.valid_from?.message}>
        <Input
          id="occ-from"
          type="date"
          aria-invalid={!!errors.valid_from}
          {...form.register('valid_from')}
        />
      </Field>
      <Field id="occ-to" label={<Trans>Until (optional)</Trans>} error={errors.valid_to?.message}>
        <Input id="occ-to" type="date" {...form.register('valid_to')} />
      </Field>
      {kind === 'owner' ? (
        <label className="flex items-center gap-2 text-sm sm:col-span-2">
          <Checkbox {...form.register('is_correspondent')} />
          <Trans>Receives the unit&apos;s correspondence</Trans>
        </label>
      ) : null}
      <div className="flex flex-col gap-3 sm:col-span-2">
        <FormError message={errors.root?.message} />
        <div>
          <SubmitButton busy={add.isPending}>
            <Trans>Link to unit</Trans>
          </SubmitButton>
        </div>
      </div>
    </form>
  )
}
