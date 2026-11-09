import { useEffect, useMemo, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { PlusIcon } from 'lucide-react'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { useSession } from '@/app/session'
import { useBookableUnits } from '@/features/bookings/BookDialog'
import { daysLabel } from '@/features/bookings/rules'
import { useWeekdayNames } from '@/features/bookings/FacilitiesPage'
import {
  gApi,
  idemHeader,
  unwrap,
  useKeyStore,
  useOrgMutation,
  useOrgQuery,
  type S,
  type SiteCtx,
} from '@/features/meetings/kit/api'
import { todayIn } from '@/features/meetings/kit/time'
import {
  applyServerErrors,
  Field,
  FormRootError,
  QueryBoundary,
  ConfirmDialog,
  Select,
} from '@/features/meetings/kit/ui'
import { normalizePlate } from './PreRegisterDialog'
import { QrOnceDialog } from './QrOnceDialog'

function NewRecurringDialog({ ctx, onClose }: { ctx: SiteCtx; onClose: () => void }) {
  const { t } = useLingui()
  const { can } = useSession()
  const idem = useIdempotencyKey()
  const days = useWeekdayNames()
  const units = useBookableUnits(ctx, can('visitors.read'))
  const schema = useMemo(
    () =>
      z
        .object({
          unit_id: z.string().min(1, t`Choose a unit.`),
          name: z
            .string()
            .trim()
            .min(2, t`Enter the person's name.`),
          plate: z.string().trim(),
          days: z.array(z.number()).min(1, t`Choose at least one day.`),
          start: z.string().regex(/^\d{2}:\d{2}$/, t`Enter a time.`),
          end: z.string().regex(/^\d{2}:\d{2}$/, t`Enter a time.`),
          valid_from: z.string().min(1, t`Choose a start date.`),
          valid_to: z.string(),
        })
        .refine((v) => v.end > v.start, {
          path: ['end'],
          message: t`The end must be after the start.`,
        })
        .refine((v) => v.valid_to === '' || v.valid_to >= v.valid_from, {
          path: ['valid_to'],
          message: t`The last day must not be before the first.`,
        }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      unit_id: units[0]?.id ?? '',
      name: '',
      plate: '',
      days: [1, 2, 3, 4, 5],
      start: '09:00',
      end: '17:00',
      valid_from: todayIn(ctx.timeZone),
      valid_to: '',
    },
  })
  const [pass, setPass] = useState<{ token: string; name: string } | null>(null)
  const firstUnit = units[0]?.id
  useEffect(() => {
    if (firstUnit && !form.getValues('unit_id')) form.setValue('unit_id', firstUnit)
  }, [firstUnit, form])
  const create = useOrgMutation(async (v: Values) =>
    unwrap(
      gApi.POST('/v1/orgs/{org_id}/recurring-visitors', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: {
          site_id: ctx.siteId,
          unit_id: v.unit_id,
          name: v.name.trim(),
          plate: v.plate === '' ? null : normalizePlate(v.plate),
          days: v.days,
          start: v.start,
          end: v.end,
          valid_from: v.valid_from,
          valid_to: v.valid_to === '' ? null : v.valid_to,
        },
      }),
    ),
  )
  const err = form.formState.errors
  if (pass)
    return (
      <QrOnceDialog
        token={pass.token}
        name={pass.name}
        validity={t`on the chosen days until you revoke it`}
        onClose={onClose}
      />
    )
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={form.handleSubmit(async (v) => {
            try {
              const r = await create.mutateAsync(v)
              if (r.qr_token) setPass({ token: r.qr_token, name: r.name })
              else onClose()
            } catch (e) {
              applyServerErrors(form, e, t`Could not save the recurring visitor.`)
            }
          })}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>New recurring visitor</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>
                For a cleaner, a carer or a nanny: a standing permission with its own long-lived QR
                code.
              </Trans>
            </DialogDescription>
          </DialogHeader>
          <Field label={t`Unit`} htmlFor="rv-unit" error={err.unit_id?.message}>
            <Select id="rv-unit" {...form.register('unit_id')}>
              {units.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.label}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t`Name`} htmlFor="rv-name" error={err.name?.message}>
            <Input id="rv-name" {...form.register('name')} />
          </Field>
          <Field label={t`Plate (optional)`} htmlFor="rv-plate">
            <Input id="rv-plate" dir="ltr" {...form.register('plate')} />
          </Field>
          <Controller
            control={form.control}
            name="days"
            render={({ field }) => (
              <fieldset className="flex flex-col gap-1">
                <legend className="text-sm font-medium">
                  <Trans>Days</Trans>
                </legend>
                <div className="flex flex-wrap gap-1">
                  {days.map((name, i) => {
                    const d = i + 1
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
                          'rounded-md border px-3 py-1.5 text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background',
                          on ? 'bg-primary text-primary-foreground' : 'hover:bg-accent',
                        )}
                      >
                        {name}
                      </button>
                    )
                  })}
                </div>
                {err.days?.message ? (
                  <p role="alert" className="text-sm text-destructive">
                    {err.days.message}
                  </p>
                ) : null}
              </fieldset>
            )}
          />
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t`From`} htmlFor="rv-start" error={err.start?.message}>
              <Input id="rv-start" type="time" dir="ltr" {...form.register('start')} />
            </Field>
            <Field label={t`Until`} htmlFor="rv-end" error={err.end?.message}>
              <Input id="rv-end" type="time" dir="ltr" {...form.register('end')} />
            </Field>
            <Field label={t`Valid from`} htmlFor="rv-vf" error={err.valid_from?.message}>
              <Input id="rv-vf" type="date" dir="ltr" {...form.register('valid_from')} />
            </Field>
            <Field label={t`Valid until (optional)`} htmlFor="rv-vt" error={err.valid_to?.message}>
              <Input id="rv-vt" type="date" dir="ltr" {...form.register('valid_to')} />
            </Field>
          </div>
          <FormRootError message={err.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={create.isPending}>
              <Trans>Save and show the code</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Recurring visitors: days and hours, a long-lived QR, revocable (C2). */
export function RecurringTab({ ctx, canManage }: { ctx: SiteCtx; canManage: boolean }) {
  const { t } = useLingui()
  const names = useWeekdayNames()
  const keys = useKeyStore()
  const [adding, setAdding] = useState(false)
  const [revoking, setRevoking] = useState<S['RecurringVisitor'] | null>(null)
  const list = useOrgQuery(['recurring', ctx.siteId], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/recurring-visitors', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items),
  )
  const revoke = useOrgMutation(async (r: S['RecurringVisitor']) => {
    await unwrap(
      gApi.POST('/v1/orgs/{org_id}/recurring-visitors/{recurring_visitor_id}:revoke', {
        params: {
          path: { org_id: ctx.orgId, recurring_visitor_id: r.id },
          header: idemHeader(keys.get(r.id)),
        },
      }),
    )
    keys.done(r.id)
  })
  return (
    <div className="flex flex-col gap-4">
      {canManage ? (
        <div>
          <Button onClick={() => setAdding(true)}>
            <PlusIcon />
            <Trans>New recurring visitor</Trans>
          </Button>
        </div>
      ) : null}
      <QueryBoundary
        query={list}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No recurring visitors`}
        emptyDescription={t`Add someone who visits on fixed days, such as a cleaner or a carer.`}
      >
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Name</Trans>
                </TableHead>
                <TableHead>
                  <Trans>When</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Valid</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Status</Trans>
                </TableHead>
                <TableHead>
                  <span className="sr-only">
                    <Trans>Actions</Trans>
                  </span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="font-medium">{r.name}</TableCell>
                  <TableCell>
                    {daysLabel(r.days, names)} {r.start}–{r.end}
                  </TableCell>
                  <TableCell>
                    <SiteDate value={r.valid_from} timeZone={ctx.timeZone} />
                    {r.valid_to ? (
                      <>
                        {' – '}
                        <SiteDate value={r.valid_to} timeZone={ctx.timeZone} />
                      </>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <Badge variant={r.status === 'active' ? 'success' : 'secondary'}>
                      {r.status === 'active'
                        ? t`Active`
                        : r.status === 'revoked'
                          ? t`Revoked`
                          : t`Expired`}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    {canManage && r.status === 'active' ? (
                      <Button size="sm" variant="outline" onClick={() => setRevoking(r)}>
                        <Trans>Revoke</Trans>
                      </Button>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>
      {adding ? <NewRecurringDialog ctx={ctx} onClose={() => setAdding(false)} /> : null}
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => (!o ? setRevoking(null) : undefined)}
        title={t`Revoke this permission?`}
        description={t`The QR code stops working at once.`}
        confirmLabel={t`Revoke`}
        destructive
        pending={revoke.isPending}
        onConfirm={() =>
          revoking &&
          revoke.mutate(revoking, {
            onSuccess: () => {
              toast.info(t`The permission was revoked.`)
              setRevoking(null)
            },
          })
        }
      />
    </div>
  )
}
