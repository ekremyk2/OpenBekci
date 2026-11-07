import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useIdempotencyKey } from '@/lib/idempotency'
import { useBookableUnits } from '@/features/bookings/BookDialog'
import { gApi, idemHeader, unwrap, useOrgMutation, type SiteCtx } from '@/features/meetings/kit/api'
import { instantToLocalInput, localInputToInstant } from '@/features/meetings/kit/time'
import { applyServerErrors, Field, FormRootError, Select } from '@/features/meetings/kit/ui'
import { QrOnceDialog } from './QrOnceDialog'

/** "34abc123" and "34 ABC 123" are the same plate. */
export const normalizePlate = (s: string) => s.replace(/[\s-]+/g, '').toUpperCase()

/** zod schema of the pre-registration form (exported for the tests). */
export function usePreRegisterSchema() {
  const { t } = useLingui()
  return useMemo(
    () =>
      z
        .object({
          unit_id: z.string().min(1, t`Choose the unit the visitor is for.`),
          name: z.string().trim().min(2, t`Enter the visitor's name.`).max(120),
          plate: z
            .string()
            .trim()
            .refine((p) => p === '' || /^[A-Za-z0-9 -]{4,12}$/.test(p), t`Enter a plate like 34 ABC 123.`),
          from: z.string().min(1, t`Choose when the visit starts.`),
          to: z.string().min(1, t`Choose when the visit ends.`),
        })
        .refine((v) => !v.from || !v.to || v.to > v.from, { path: ['to'], message: t`The visit must end after it starts.` }),
    [t],
  )
}

export function PreRegisterDialog({ ctx, onClose }: { ctx: SiteCtx; onClose: () => void }) {
  const { t } = useLingui()
  const { can } = useSession()
  const idem = useIdempotencyKey()
  const manager = can('visitors.read') && can('visitors.manage')
  const units = useBookableUnits(ctx, manager)
  const schema = usePreRegisterSchema()
  const now = new Date()
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: {
      unit_id: units[0]?.id ?? '',
      name: '',
      plate: '',
      from: instantToLocalInput(now.toISOString(), ctx.timeZone),
      to: instantToLocalInput(new Date(now.getTime() + 4 * 3_600_000).toISOString(), ctx.timeZone),
    },
  })
  const [pass, setPass] = useState<{ token: string; name: string; from: string; to: string } | null>(null)
  const firstUnit = units[0]?.id
  useEffect(() => {
    if (firstUnit && !form.getValues('unit_id')) form.setValue('unit_id', firstUnit)
  }, [firstUnit, form])
  const create = useOrgMutation(async (v: z.infer<typeof schema>) =>
    unwrap(
      gApi.POST('/v1/orgs/{org_id}/visitor-entries', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: {
          site_id: ctx.siteId,
          unit_id: v.unit_id,
          name: v.name.trim(),
          plate: v.plate.trim() === '' ? null : normalizePlate(v.plate),
          window_from: localInputToInstant(v.from, ctx.timeZone),
          window_to: localInputToInstant(v.to, ctx.timeZone),
        },
      }),
    ),
  )
  if (pass) {
    return (
      <QrOnceDialog
        token={pass.token}
        name={pass.name}
        validity={`${new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZone: ctx.timeZone }).format(new Date(pass.from))} – ${new Intl.DateTimeFormat(undefined, { timeStyle: 'short', timeZone: ctx.timeZone }).format(new Date(pass.to))}`}
        onClose={onClose}
      />
    )
  }
  const err = form.formState.errors
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={form.handleSubmit(async (v) => {
            try {
              const e = await create.mutateAsync(v)
              if (e.qr_token) setPass({ token: e.qr_token, name: e.name, from: e.window_from, to: e.window_to })
              else onClose()
            } catch (e) {
              applyServerErrors(form, e, t`Could not register the visitor.`)
            }
          })}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>Pre-register a visitor</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>You get a QR code to send to your guest. The guard scans it at the gate.</Trans>
            </DialogDescription>
          </DialogHeader>
          <Field label={t`Unit`} htmlFor="pr-unit" error={err.unit_id?.message}>
            <Select id="pr-unit" {...form.register('unit_id')}>
              {units.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.label}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t`Visitor's name`} htmlFor="pr-name" error={err.name?.message}>
            <Input id="pr-name" autoComplete="off" {...form.register('name')} />
          </Field>
          <Field label={t`Plate (optional)`} htmlFor="pr-plate" error={err.plate?.message}>
            <Input id="pr-plate" dir="ltr" autoComplete="off" {...form.register('plate')} />
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t`From`} htmlFor="pr-from" error={err.from?.message}>
              <Input id="pr-from" type="datetime-local" dir="ltr" {...form.register('from')} />
            </Field>
            <Field label={t`Until`} htmlFor="pr-to" error={err.to?.message}>
              <Input id="pr-to" type="datetime-local" dir="ltr" {...form.register('to')} />
            </Field>
          </div>
          <p className="text-xs text-muted-foreground">
            <Trans>The code also works for 30 minutes before and after this window, once.</Trans>
          </p>
          <FormRootError message={err.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={create.isPending}>
              <Trans>Create the pass</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

