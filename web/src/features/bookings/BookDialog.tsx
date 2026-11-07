import { useMemo, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { useSession } from '@/app/session'
import { Money } from '@/components/data/Money'
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
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import { useUnits } from '@/features/meetings/hooks'
import { gApi, idemHeader, unwrap, useOrgMutation, type S, type SiteCtx } from '@/features/meetings/kit/api'
import { localInputToInstant, startOfMonth, startOfWeek } from '@/features/meetings/kit/time'
import { Field, FormRootError, Select } from '@/features/meetings/kit/ui'
import { Callout } from '@/features/meetings/kit/widgets'
import { validateBooking, type BookingIssue } from './rules'

/** Units the signed-in user may book for: their own occupancies (residents) or any unit (managers). */
export function useBookableUnits(ctx: SiteCtx, manager: boolean) {
  const { me, org } = useSession()
  const units = useUnits(ctx.orgId, ctx.siteId)
  if (manager) {
    return (units.data ?? []).map((u) => ({ id: u.id, label: u.door_number, kind: 'owner' as 'owner' | 'tenant' | 'household' }))
  }
  const seen = new Map<string, { id: string; label: string; kind: 'owner' | 'tenant' | 'household' }>()
  for (const o of me?.occupancies ?? []) {
    if (o.organization_id === org?.id && o.site_id === ctx.siteId && !seen.has(o.unit_id))
      seen.set(o.unit_id, { id: o.unit_id, label: o.unit_label, kind: o.kind as 'owner' | 'tenant' | 'household' })
  }
  return [...seen.values()]
}

export function useIssueText() {
  const { t } = useLingui()
  return (rules: S['FacilityRules'], issue: BookingIssue): string => {
    switch (issue) {
      case 'end_before_start':
        return t`The end must be after the start.`
      case 'too_long':
        return t`A booking can last at most ${rules.max_duration_minutes} minutes.`
      case 'too_soon':
        return t`Bookings need at least ${rules.min_notice_minutes ?? 0} minutes of notice.`
      case 'too_far_ahead':
        return t`You can book at most ${rules.max_days_ahead} days ahead.`
      case 'outside_hours':
        return t`The facility is closed at that time.`
      case 'quota_exceeded':
        return t`Your unit has used its bookings for this period.`
      case 'not_allowed':
        return t`This facility is not open to your kind of occupant.`
    }
  }
}

/** Book a facility: rules are checked as you type, the fee is shown, the booking confirms at once. */
export function BookDialog({
  ctx,
  facilities,
  bookings,
  initialFacilityId,
  initialDate,
  onClose,
}: {
  ctx: SiteCtx
  facilities: S['Facility'][]
  bookings: S['Booking'][]
  initialFacilityId?: string
  initialDate: string
  onClose: () => void
}) {
  const { t } = useLingui()
  const { can } = useSession()
  const manager = can('bookings.manage')
  const idem = useIdempotencyKey()
  const unitOptions = useBookableUnits(ctx, manager)
  const issueText = useIssueText()
  const open = facilities.filter((f) => f.enabled)
  const [facilityId, setFacilityId] = useState(initialFacilityId || open[0]?.id || '')
  const [unitId, setUnitId] = useState(unitOptions[0]?.id ?? '')
  const [date, setDate] = useState(initialDate)
  const [from, setFrom] = useState('18:00')
  const [to, setTo] = useState('20:00')
  const [debtor, setDebtor] = useState<'owner' | 'tenant'>('owner')
  const [error, setError] = useState<string | null>(null)
  const facility = open.find((f) => f.id === facilityId)
  const unit = unitOptions.find((u) => u.id === unitId) ?? unitOptions[0]

  const start = date && from ? localInputToInstant(`${date}T${from}`, ctx.timeZone) : ''
  const end = date && to ? localInputToInstant(`${date}T${to}`, ctx.timeZone) : ''
  const used = useMemo(() => {
    if (!facility || !start) return 0
    const period =
      facility.rules.quota_period === 'week'
        ? [startOfWeek(date), 7]
        : [startOfMonth(date), 31]
    const lo = localInputToInstant(`${period[0]}T00:00`, ctx.timeZone)
    const hi = Date.parse(lo) + Number(period[1]) * 86_400_000
    return bookings.filter(
      (b) =>
        b.facility_id === facility.id &&
        b.unit_id === unit?.id &&
        b.status === 'confirmed' &&
        Date.parse(b.start) >= Date.parse(lo) &&
        Date.parse(b.start) < hi,
    ).length
  }, [bookings, facility, unit?.id, date, start, ctx.timeZone])
  const issues =
    facility && start && end
      ? validateBooking({
          rules: facility.rules,
          start,
          end,
          timeZone: ctx.timeZone,
          usedInPeriod: used,
          bookerKind: manager ? undefined : unit?.kind,
        })
      : []

  const create = useOrgMutation(async () =>
    unwrap(
      gApi.POST('/v1/orgs/{org_id}/bookings', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: {
          facility_id: facilityId,
          unit_id: unit!.id,
          start,
          end,
          ...(manager && facility?.rules.fee ? { debtor_kind: debtor } : {}),
        },
      }),
    ),
  )

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (!facility || !unit || issues.length > 0) return
            setError(null)
            create.mutate(undefined, {
              onSuccess: () => {
                toast.info(t`Booked. Your booking is confirmed.`)
                onClose()
              },
              onError: (err) =>
                setError(
                  err instanceof AppError && err.status === 409
                    ? t`That time is already taken or blocked. Choose another slot.`
                    : err instanceof AppError && err.isValidation
                      ? (Object.values(err.fieldErrors)[0]?.[0]?.message ?? t`The booking breaks a rule of this facility.`)
                      : t`Could not book. Please try again.`,
                ),
            })
          }}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>Book a facility</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>Inside the rules, your booking is confirmed immediately.</Trans>
            </DialogDescription>
          </DialogHeader>
          <Field label={t`Facility`} htmlFor="bk-fac">
            <Select id="bk-fac" value={facilityId} onChange={(e) => setFacilityId(e.target.value)}>
              {open.map((f) => (
                <option key={f.id} value={f.id}>
                  {f.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t`Unit`} htmlFor="bk-unit">
            <Select id="bk-unit" value={unit?.id ?? ''} onChange={(e) => setUnitId(e.target.value)}>
              {unitOptions.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.label}
                </option>
              ))}
            </Select>
          </Field>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label={t`Date`} htmlFor="bk-date">
              <Input id="bk-date" type="date" dir="ltr" value={date} onChange={(e) => setDate(e.target.value)} />
            </Field>
            <Field label={t`From`} htmlFor="bk-from">
              <Input id="bk-from" type="time" dir="ltr" value={from} onChange={(e) => setFrom(e.target.value)} />
            </Field>
            <Field label={t`Until`} htmlFor="bk-to">
              <Input id="bk-to" type="time" dir="ltr" value={to} onChange={(e) => setTo(e.target.value)} />
            </Field>
          </div>
          {facility?.rules.fee ? (
            <Callout tone="info" role="status">
              <Trans>
                Fee: <Money minor={facility.rules.fee.amount} currency={facility.rules.fee.currency} />, charged to
                the unit's account when the booking is confirmed.
                {facility.rules.cancel_refund_cutoff_hours !== null
                  ? ` Refunded if you cancel at least ${facility.rules.cancel_refund_cutoff_hours} hours ahead.`
                  : ''}
              </Trans>
            </Callout>
          ) : null}
          {manager && facility?.rules.fee ? (
            <Field label={t`Charge to`} htmlFor="bk-debtor">
              <Select id="bk-debtor" value={debtor} onChange={(e) => setDebtor(e.target.value as 'owner' | 'tenant')}>
                <option value="owner">{t`Owner`}</option>
                <option value="tenant">{t`Tenant`}</option>
              </Select>
            </Field>
          ) : null}
          {facility && issues.length > 0 ? (
            <Callout tone="warning" role="alert">
              <ul className="list-disc ps-5">
                {issues.map((i) => (
                  <li key={i}>{issueText(facility.rules, i)}</li>
                ))}
              </ul>
            </Callout>
          ) : null}
          <FormRootError message={error ?? undefined} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={!facility || !unit || issues.length > 0 || create.isPending}>
              <Trans>Book now</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
