import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { Controller, useFieldArray, useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { PlusIcon, Trash2Icon } from 'lucide-react'
import { useLocale } from '@/app/locale'
import { useSession } from '@/app/session'
import { Money } from '@/components/data/Money'
import { MoneyInput } from '@/components/forms/MoneyInput'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import {
  gApi,
  idemHeader,
  ifMatch,
  unwrap,
  useOrgMutation,
  useOrgQuery,
  useSiteCtx,
  type S,
  type SiteCtx,
} from '@/features/meetings/kit/api'
import {
  applyServerErrors,
  LabeledCheckbox,
  Field,
  FormRootError,
  PageHeader,
  QueryBoundary,
  ConfirmDialog,
  Select,
  Textarea,
} from '@/features/meetings/kit/ui'
import { daysLabel } from './rules'

type Rules = S['FacilityRules']

export function useWeekdayNames(): string[] {
  const { intl } = useLocale()
  return useMemo(() => {
    const f = new Intl.DateTimeFormat(intl, { weekday: 'short', timeZone: 'UTC' })
    // 2024-01-01 was a Monday
    return Array.from({ length: 7 }, (_, i) => f.format(new Date(Date.UTC(2024, 0, 1 + i))))
  }, [intl])
}

export function useFacilities(ctx: SiteCtx) {
  return useOrgQuery(['facilities', ctx.siteId], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/facilities', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items),
  )
}

const hhmm = /^\d{2}:\d{2}$/

/** zod schema of the facility form (rules editor). */
export function useFacilitySchema() {
  const { t } = useLingui()
  return useMemo(() => {
    const int = (min: number, max: number, optional = false) =>
      z
        .string()
        .trim()
        .refine(
          (s) =>
            (optional && s === '') || (/^\d+$/.test(s) && Number(s) >= min && Number(s) <= max),
          t`Enter a whole number from ${min} to ${max}.`,
        )
    return z.object({
      name: z
        .string()
        .trim()
        .min(1, t`Enter a name.`),
      description: z.string(),
      enabled: z.boolean(),
      hours: z
        .array(
          z
            .object({
              days: z.array(z.number()).min(1, t`Choose at least one day.`),
              start: z.string().regex(hhmm, t`Enter a time.`),
              end: z.string().regex(hhmm, t`Enter a time.`),
            })
            .refine((h) => h.end > h.start, {
              path: ['end'],
              message: t`Closing must be after opening.`,
            }),
        )
        .min(1, t`Add the opening hours.`),
      max_duration_minutes: int(5, 1440),
      max_days_ahead: int(0, 365),
      min_notice_minutes: int(0, 100000, true),
      quota_per_unit: int(0, 1000, true),
      quota_period: z.enum(['week', 'month']),
      who_may_book: z
        .array(z.enum(['owner', 'tenant', 'household']))
        .min(1, t`Choose who may book.`),
      fee: z.custom<bigint | null>((v) => v === null || (typeof v === 'bigint' && v >= 0n)),
      cancel_refund_cutoff_hours: int(0, 10000, true),
    })
  }, [t])
}
export type FacilityValues = z.infer<ReturnType<typeof useFacilitySchema>>

export const defaultFacility = (): FacilityValues => ({
  name: '',
  description: '',
  enabled: true,
  hours: [{ days: [1, 2, 3, 4, 5, 6, 7], start: '09:00', end: '22:00' }],
  max_duration_minutes: '120',
  max_days_ahead: '30',
  min_notice_minutes: '60',
  quota_per_unit: '',
  quota_period: 'month',
  who_may_book: ['owner', 'tenant', 'household'],
  fee: null,
  cancel_refund_cutoff_hours: '24',
})

const optInt = (s: string) => (s.trim() === '' ? null : Number(s))

export function toRules(v: FacilityValues, currency: string): Rules {
  return {
    opening_hours: v.hours,
    max_duration_minutes: Number(v.max_duration_minutes),
    max_days_ahead: Number(v.max_days_ahead),
    min_notice_minutes: optInt(v.min_notice_minutes),
    quota_per_unit: optInt(v.quota_per_unit),
    quota_period: optInt(v.quota_per_unit) === null ? null : v.quota_period,
    who_may_book: v.who_may_book,
    fee: v.fee !== null && v.fee > 0n ? { amount: Number(v.fee), currency } : null,
    cancel_refund_cutoff_hours: optInt(v.cancel_refund_cutoff_hours),
  }
}

export function fromFacility(f: S['Facility']): FacilityValues {
  const r = f.rules
  return {
    name: f.name,
    description: f.description ?? '',
    enabled: f.enabled,
    hours: r.opening_hours.map((h) => ({ ...h })),
    max_duration_minutes: String(r.max_duration_minutes),
    max_days_ahead: String(r.max_days_ahead),
    min_notice_minutes: r.min_notice_minutes === null ? '' : String(r.min_notice_minutes),
    quota_per_unit: r.quota_per_unit === null ? '' : String(r.quota_per_unit),
    quota_period: r.quota_period ?? 'month',
    who_may_book: r.who_may_book,
    fee: r.fee ? BigInt(r.fee.amount) : null,
    cancel_refund_cutoff_hours:
      r.cancel_refund_cutoff_hours === null ? '' : String(r.cancel_refund_cutoff_hours),
  }
}

function FacilityDialog({
  ctx,
  facility,
  onClose,
}: {
  ctx: SiteCtx
  facility: S['Facility'] | null
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const days = useWeekdayNames()
  const schema = useFacilitySchema()
  const form = useForm<FacilityValues>({
    resolver: zodResolver(schema),
    defaultValues: facility ? fromFacility(facility) : defaultFacility(),
  })
  const hours = useFieldArray({ control: form.control, name: 'hours' })
  const err = form.formState.errors
  const save = useOrgMutation(async (v: FacilityValues) => {
    const body = {
      name: v.name.trim(),
      description: v.description.trim() === '' ? null : v.description.trim(),
      enabled: v.enabled,
      rules: toRules(v, ctx.currency),
    }
    if (facility) {
      return unwrap(
        gApi.PATCH('/v1/orgs/{org_id}/facilities/{facility_id}', {
          params: {
            path: { org_id: ctx.orgId, facility_id: facility.id },
            header: ifMatch(facility.version),
          },
          body,
        }),
      )
    }
    return unwrap(
      gApi.POST('/v1/orgs/{org_id}/facilities', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: { site_id: ctx.siteId, ...body },
      }),
    )
  })
  const who = useWatch({ control: form.control, name: 'who_may_book' })
  const whoNames = { owner: t`Owners`, tenant: t`Tenants`, household: t`Household members` }
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent className="sm:max-w-2xl">
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={form.handleSubmit(async (v) => {
            try {
              await save.mutateAsync(v)
              toast.info(t`Facility saved.`)
              onClose()
            } catch (e) {
              applyServerErrors(form, e, t`Could not save the facility.`)
            }
          })}
        >
          <DialogHeader>
            <DialogTitle>{facility ? t`Edit facility` : t`New facility`}</DialogTitle>
            <DialogDescription>
              <Trans>
                A booking inside these rules is confirmed at once; there is no approval queue.
              </Trans>
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t`Name`} htmlFor="fc-name" error={err.name?.message}>
              <Input id="fc-name" {...form.register('name')} />
            </Field>
            <div className="flex items-end">
              <LabeledCheckbox label={t`Open for booking`} {...form.register('enabled')} />
            </div>
            <Field label={t`Description`} htmlFor="fc-desc" className="sm:col-span-2">
              <Textarea id="fc-desc" {...form.register('description')} />
            </Field>
          </div>
          <fieldset className="flex flex-col gap-2">
            <legend className="text-sm font-medium">
              <Trans>Opening hours</Trans>
            </legend>
            {hours.fields.map((h, i) => (
              <div key={h.id} className="flex flex-col gap-2 rounded-md border p-2">
                <Controller
                  control={form.control}
                  name={`hours.${i}.days`}
                  render={({ field }) => (
                    <div role="group" aria-label={t`Days`} className="flex flex-wrap gap-1">
                      {days.map((name, di) => {
                        const d = di + 1
                        const on = field.value.includes(d)
                        return (
                          <button
                            key={d}
                            type="button"
                            aria-pressed={on}
                            onClick={() =>
                              field.onChange(
                                on ? field.value.filter((x) => x !== d) : [...field.value, d],
                              )
                            }
                            className={cn(
                              'rounded-md border px-2 py-1 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background',
                              on ? 'bg-primary text-primary-foreground' : 'hover:bg-accent',
                            )}
                          >
                            {name}
                          </button>
                        )
                      })}
                    </div>
                  )}
                />
                <div className="flex flex-wrap items-center gap-2">
                  <Input
                    aria-label={t`Opens`}
                    type="time"
                    dir="ltr"
                    className="w-auto"
                    {...form.register(`hours.${i}.start`)}
                  />
                  <span>–</span>
                  <Input
                    aria-label={t`Closes`}
                    type="time"
                    dir="ltr"
                    className="w-auto"
                    {...form.register(`hours.${i}.end`)}
                  />
                  {hours.fields.length > 1 ? (
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      aria-label={t`Remove hours`}
                      onClick={() => hours.remove(i)}
                    >
                      <Trash2Icon />
                    </Button>
                  ) : null}
                </div>
                {err.hours?.[i]?.days?.message || err.hours?.[i]?.end?.message ? (
                  <p role="alert" className="text-sm text-destructive">
                    {err.hours?.[i]?.days?.message ?? err.hours?.[i]?.end?.message}
                  </p>
                ) : null}
              </div>
            ))}
            <div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => hours.append({ days: [6, 7], start: '10:00', end: '18:00' })}
              >
                <PlusIcon />
                <Trans>Add hours</Trans>
              </Button>
            </div>
          </fieldset>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field
              label={t`Longest booking (minutes)`}
              htmlFor="fc-dur"
              error={err.max_duration_minutes?.message}
            >
              <Input
                id="fc-dur"
                inputMode="numeric"
                dir="ltr"
                {...form.register('max_duration_minutes')}
              />
            </Field>
            <Field
              label={t`How far ahead (days)`}
              htmlFor="fc-ahead"
              error={err.max_days_ahead?.message}
            >
              <Input
                id="fc-ahead"
                inputMode="numeric"
                dir="ltr"
                {...form.register('max_days_ahead')}
              />
            </Field>
            <Field
              label={t`Shortest notice (minutes)`}
              htmlFor="fc-notice"
              error={err.min_notice_minutes?.message}
            >
              <Input
                id="fc-notice"
                inputMode="numeric"
                dir="ltr"
                {...form.register('min_notice_minutes')}
              />
            </Field>
            <div className="grid grid-cols-2 gap-2">
              <Field
                label={t`Bookings per unit`}
                htmlFor="fc-quota"
                error={err.quota_per_unit?.message}
                hint={t`Empty: no limit`}
              >
                <Input
                  id="fc-quota"
                  inputMode="numeric"
                  dir="ltr"
                  {...form.register('quota_per_unit')}
                />
              </Field>
              <Field label={t`per`} htmlFor="fc-period">
                <Select id="fc-period" {...form.register('quota_period')}>
                  <option value="week">{t`week`}</option>
                  <option value="month">{t`month`}</option>
                </Select>
              </Field>
            </div>
          </div>
          <fieldset className="flex flex-col gap-1">
            <legend className="text-sm font-medium">
              <Trans>Who may book</Trans>
            </legend>
            <div className="flex flex-wrap gap-4">
              {(['owner', 'tenant', 'household'] as const).map((k) => (
                <LabeledCheckbox
                  key={k}
                  label={whoNames[k]}
                  checked={who.includes(k)}
                  onChange={(e) =>
                    form.setValue(
                      'who_may_book',
                      e.target.checked ? [...who, k] : who.filter((x) => x !== k),
                      {
                        shouldValidate: true,
                      },
                    )
                  }
                />
              ))}
            </div>
            {err.who_may_book?.message ? (
              <p role="alert" className="text-sm text-destructive">
                {err.who_may_book.message}
              </p>
            ) : null}
          </fieldset>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field
              label={t`Fee per booking`}
              htmlFor="fc-fee"
              hint={t`Becomes a charge when the booking is confirmed. Empty: free.`}
            >
              <Controller
                control={form.control}
                name="fee"
                render={({ field }) => (
                  <MoneyInput
                    id="fc-fee"
                    currency={ctx.currency}
                    value={field.value}
                    onChange={field.onChange}
                  />
                )}
              />
            </Field>
            <Field
              label={t`Fee refunded if cancelled this many hours before`}
              htmlFor="fc-cutoff"
              error={err.cancel_refund_cutoff_hours?.message}
            >
              <Input
                id="fc-cutoff"
                inputMode="numeric"
                dir="ltr"
                {...form.register('cancel_refund_cutoff_hours')}
              />
            </Field>
          </div>
          <FormRootError message={err.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={save.isPending}>
              <Trans>Save</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function RulesSummary({ rules }: { rules: Rules }) {
  const { t } = useLingui()
  const names = useWeekdayNames()
  return (
    <dl className="grid gap-x-4 gap-y-1 text-sm sm:grid-cols-2">
      <div className="flex gap-2">
        <dt className="text-muted-foreground">{t`Hours`}</dt>
        <dd>
          {rules.opening_hours
            .map((h) => `${daysLabel(h.days, names)} ${h.start}–${h.end}`)
            .join('; ')}
        </dd>
      </div>
      <div className="flex gap-2">
        <dt className="text-muted-foreground">{t`Longest`}</dt>
        <dd>{t`${rules.max_duration_minutes} min`}</dd>
      </div>
      <div className="flex gap-2">
        <dt className="text-muted-foreground">{t`Ahead`}</dt>
        <dd>{t`${rules.max_days_ahead} days`}</dd>
      </div>
      <div className="flex gap-2">
        <dt className="text-muted-foreground">{t`Quota`}</dt>
        <dd>
          {rules.quota_per_unit === null
            ? t`none`
            : rules.quota_period === 'week'
              ? t`${rules.quota_per_unit} per week`
              : t`${rules.quota_per_unit} per month`}
        </dd>
      </div>
      <div className="flex gap-2">
        <dt className="text-muted-foreground">{t`Fee`}</dt>
        <dd>
          {rules.fee ? <Money minor={rules.fee.amount} currency={rules.fee.currency} /> : t`free`}
        </dd>
      </div>
    </dl>
  )
}

/** Facility list with the rules editor (C1). */
export function FacilitiesPage() {
  const { t } = useLingui()
  const { can } = useSession()
  const ctx = useSiteCtx()!
  const manage = can('facilities.manage')
  const facilities = useFacilities(ctx)
  const [editing, setEditing] = useState<S['Facility'] | 'new' | null>(null)
  const [deleting, setDeleting] = useState<S['Facility'] | null>(null)
  const remove = useOrgMutation(async (f: S['Facility']) => {
    await gApi.DELETE('/v1/orgs/{org_id}/facilities/{facility_id}', {
      params: { path: { org_id: ctx.orgId, facility_id: f.id }, header: ifMatch(f.version) },
    })
  })
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Facilities`}
        description={t`Halls, courts and barbecue areas, each with its own booking rules.`}
        actions={
          <>
            <Link
              to="/orgs/$orgId/bookings"
              params={{ orgId: ctx.orgId }}
              className={buttonVariants({ variant: 'outline' })}
            >
              <Trans>Bookings</Trans>
            </Link>
            {manage ? (
              <Button onClick={() => setEditing('new')}>
                <PlusIcon />
                <Trans>New facility</Trans>
              </Button>
            ) : null}
          </>
        }
      />
      <QueryBoundary
        query={facilities}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No facilities yet`}
        emptyDescription={manage ? t`Add the first facility residents can book.` : undefined}
      >
        {(items) => (
          <div className="grid gap-4 md:grid-cols-2">
            {items.map((f) => (
              <Card key={f.id}>
                <CardHeader className="flex flex-row items-center justify-between gap-2">
                  <CardTitle className="text-base">{f.name}</CardTitle>
                  <Badge variant={f.enabled ? 'success' : 'secondary'}>
                    {f.enabled ? t`Open` : t`Closed`}
                  </Badge>
                </CardHeader>
                <CardContent className="flex flex-col gap-3">
                  {f.description ? (
                    <p className="text-sm text-muted-foreground">{f.description}</p>
                  ) : null}
                  <RulesSummary rules={f.rules} />
                  {manage ? (
                    <div className="flex gap-2">
                      <Button size="sm" variant="outline" onClick={() => setEditing(f)}>
                        <Trans>Edit rules</Trans>
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => setDeleting(f)}>
                        <Trans>Delete</Trans>
                      </Button>
                    </div>
                  ) : null}
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </QueryBoundary>
      {editing ? (
        <FacilityDialog
          ctx={ctx}
          facility={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
        />
      ) : null}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => (!o ? setDeleting(null) : undefined)}
        title={t`Delete this facility?`}
        description={t`Existing bookings stay in the history.`}
        confirmLabel={t`Delete`}
        destructive
        pending={remove.isPending}
        onConfirm={() =>
          deleting && remove.mutate(deleting, { onSuccess: () => setDeleting(null) })
        }
      />
    </div>
  )
}
