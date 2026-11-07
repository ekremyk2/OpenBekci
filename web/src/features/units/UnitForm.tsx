import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, ifMatch, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { UnitPicker, type PickerOption } from '@/components/forms/EntityPicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { siteQueries } from '@/features/sites/queries'
import { unitTypeLabel } from './labels'
import { UNIT_TYPES } from './queries'

const APPURTENANCE_TYPES = ['parking', 'storage']

/** Create (no `unit`) or edit a unit; parking and storage can be an appurtenance of a flat (S2). */
export function UnitForm({
  orgId,
  siteId,
  unit,
  parentLabel,
  onSaved,
}: {
  orgId: string
  siteId: string
  unit?: Schema<'Unit'>
  /** Door number of the parent flat, when editing an appurtenance. */
  parentLabel?: string
  onSaved?: (unit: Schema<'Unit'>) => void
}) {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const blocksQ = useQuery(siteQueries.blocks(orgId, siteId))
  const settingsQ = useQuery(siteQueries.settings(orgId, siteId))
  const blocks = blocksQ.data?.items ?? []
  const singleBlock = blocks.length === 1 && blocks[0]!.is_default
  const [parent, setParent] = useState<PickerOption | null>(
    unit?.parent_unit_id
      ? { id: unit.parent_unit_id, label: parentLabel ?? unit.parent_unit_id }
      : null,
  )

  // who pays which charge type on this unit when it differs from the default (owner or tenant)
  const [overrides, setOverrides] = useState<{ charge: string; side: string }[]>(() =>
    Object.entries(unit?.payer_overrides ?? {}).map(([charge, side]) => ({ charge, side })),
  )

  const schema = useMemo(
    () =>
      z.object({
        door_number: z
          .string()
          .trim()
          .min(1, t`Enter the door number, for example A-12`),
        legal_ref: z.string().trim(),
        type: z.string().min(1),
        area_m2: z
          .string()
          .trim()
          .refine(
            (v) =>
              v === '' ||
              (!Number.isNaN(Number(v.replace(',', '.'))) && Number(v.replace(',', '.')) >= 0),
            t`Enter the area in square metres`,
          ),
        block_id: z.string(),
      }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: {
      door_number: unit?.door_number ?? '',
      legal_ref: unit?.legal_ref ?? '',
      type: unit?.type ?? 'flat',
      area_m2: unit?.area_m2 == null ? '' : String(unit.area_m2),
      block_id: unit?.block_id ?? '',
    },
  })
  const type = form.watch('type')
  const showParent = APPURTENANCE_TYPES.includes(type)
  const defaultAppurtenance = settingsQ.data?.parking_storage_default !== 'independent'
  const { errors } = form.formState

  const save = useMutation({
    mutationFn: (v: z.infer<typeof schema>) => {
      const blockId = v.block_id || blocks[0]?.id || ''
      const area = v.area_m2 === '' ? null : Number(v.area_m2.replace(',', '.'))
      const parent_unit_id = showParent ? (parent?.id ?? null) : null
      const payer_overrides = Object.fromEntries(
        overrides
          .filter((o) => o.charge.trim())
          .map((o) => [o.charge.trim(), o.side as 'owner' | 'tenant']),
      )
      return runWithReauth(async () => {
        if (unit) {
          const { data } = await draftApi.PATCH('/v1/orgs/{org_id}/units/{unit_id}', {
            params: { path: { org_id: orgId, unit_id: unit.id }, header: ifMatch(unit.version) },
            body: {
              block_id: blockId,
              door_number: v.door_number,
              legal_ref: v.legal_ref || null,
              type: v.type as Schema<'Unit'>['type'],
              area_m2: area,
              parent_unit_id,
              payer_overrides,
            },
          })
          return data!
        }
        const { data } = await draftApi.POST('/v1/orgs/{org_id}/units', {
          params: { path: { org_id: orgId } },
          body: {
            site_id: siteId,
            block_id: blockId,
            door_number: v.door_number,
            legal_ref: v.legal_ref || null,
            type: v.type as Schema<'Unit'>['type'],
            area_m2: area,
            parent_unit_id,
            payer_overrides,
          },
          ...withIdempotencyKey(idem.key),
        })
        return data!
      })
    },
    onSuccess: async (saved) => {
      idem.renew()
      await queryClient.invalidateQueries({ queryKey: ['tenancy.listUnits', orgId] })
      await queryClient.invalidateQueries({ queryKey: ['tenancy.getUnit', orgId] })
      onSaved?.(saved)
    },
    onError: (e) => applyError(form, e),
  })

  return (
    <form
      noValidate
      className="grid max-w-2xl gap-4 sm:grid-cols-2"
      onSubmit={form.handleSubmit((v) => save.mutateAsync(v).catch(() => undefined))}
    >
      <Field
        id="unit-door"
        label={<Trans>Door number</Trans>}
        error={errors.door_number?.message}
        hint={<Trans>What people say, for example A-12.</Trans>}
      >
        <Input
          id="unit-door"
          aria-invalid={!!errors.door_number}
          {...form.register('door_number')}
        />
      </Field>
      <Field
        id="unit-legal"
        label={<Trans>Title deed no. (optional)</Trans>}
        error={errors.legal_ref?.message}
        hint={<Trans>Independent section number from the title deed (tapu).</Trans>}
      >
        <Input id="unit-legal" {...form.register('legal_ref')} />
      </Field>
      <Field id="unit-type" label={<Trans>Type</Trans>} error={errors.type?.message}>
        <Select id="unit-type" {...form.register('type')}>
          {UNIT_TYPES.map((x) => (
            <option key={x} value={x}>
              {unitTypeLabel(x, i18n)}
            </option>
          ))}
        </Select>
      </Field>
      <Field id="unit-area" label={<Trans>Area (m²)</Trans>} error={errors.area_m2?.message}>
        <Input
          id="unit-area"
          inputMode="decimal"
          aria-invalid={!!errors.area_m2}
          {...form.register('area_m2')}
        />
      </Field>
      {singleBlock ? null : (
        <Field id="unit-block" label={<Trans>Block</Trans>} error={errors.block_id?.message}>
          <Select id="unit-block" {...form.register('block_id')}>
            <option value="">{t`Choose a block`}</option>
            {blocks.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </Select>
        </Field>
      )}
      {showParent ? (
        <Field
          id="unit-parent"
          label={<Trans>Belongs to flat</Trans>}
          hint={
            defaultAppurtenance ? (
              <Trans>
                By default parking and storage belong to a flat and have no dues of their own. Clear
                this to make it an independent unit.
              </Trans>
            ) : (
              <Trans>
                This site treats parking and storage as independent units. Choose a flat only if
                this one is an appurtenance.
              </Trans>
            )
          }
          className="sm:col-span-2"
        >
          <UnitPicker
            id="unit-parent"
            orgId={orgId}
            siteId={siteId}
            value={parent}
            onChange={setParent}
          />
        </Field>
      ) : null}
      <fieldset className="grid gap-2 sm:col-span-2">
        <legend className="text-sm font-medium">
          <Trans>Who pays what on this unit</Trans>
        </legend>
        <p className="text-xs text-muted-foreground">
          <Trans>
            Charges go to the default payer (the tenant for regular dues, the owner for special
            assessments). Add a line only where this unit differs.
          </Trans>
        </p>
        {overrides.map((o, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <Input
              aria-label={t`Charge type`}
              className="w-40"
              dir="ltr"
              placeholder="dues"
              value={o.charge}
              onChange={(e) =>
                setOverrides((l) =>
                  l.map((x, j) => (j === i ? { ...x, charge: e.target.value } : x)),
                )
              }
            />
            <Select
              aria-label={t`Pays`}
              className="w-auto"
              value={o.side}
              onChange={(e) =>
                setOverrides((l) => l.map((x, j) => (j === i ? { ...x, side: e.target.value } : x)))
              }
            >
              <option value="owner">{t`Owner`}</option>
              <option value="tenant">{t`Tenant`}</option>
            </Select>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setOverrides((l) => l.filter((_, j) => j !== i))}
            >
              <Trans>Remove</Trans>
            </Button>
          </div>
        ))}
        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setOverrides((l) => [...l, { charge: '', side: 'owner' }])}
          >
            <Trans>Add an exception</Trans>
          </Button>
        </div>
      </fieldset>
      <div className="flex flex-col gap-3 sm:col-span-2">
        <FormError message={errors.root?.message} />
        <div>
          <SubmitButton busy={save.isPending}>
            {unit ? <Trans>Save changes</Trans> : <Trans>Create unit</Trans>}
          </SubmitButton>
        </div>
      </div>
    </form>
  )
}
