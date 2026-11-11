import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useQueries } from '@tanstack/react-query'
import { ChevronLeftIcon, ChevronRightIcon, PlusIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { useParams } from '@tanstack/react-router'
import { useLocale } from '@/app/locale'
import { useSession } from '@/app/session'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
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
import { cn } from '@/lib/utils'
import { toast } from '@/lib/toast'
import {
  gApi,
  idemHeader,
  okey,
  unwrap,
  useKeyStore,
  useOrgMutation,
  useOrgQuery,
  useSiteCtx,
  type S,
  type SiteCtx,
} from '@/features/meetings/kit/api'
import {
  addDays,
  instantToLocalInput,
  localInputToInstant,
  startOfMonth,
  startOfWeek,
  todayIn,
} from '@/features/meetings/kit/time'
import { Field, FormRootError, PageHeader, QueryBoundary, Select } from '@/features/meetings/kit/ui'
import { Callout } from '@/features/meetings/kit/widgets'
import { BookDialog } from './BookDialog'
import { useFacilities, useWeekdayNames } from './FacilitiesPage'
import { refundOnCancel } from './rules'

type View = 'week' | 'month' | 'list'

interface Slot {
  key: string
  start: string
  end: string
  kind: 'booking' | 'taken' | 'blocked'
  label: string
  booking?: S['Booking']
}

function rangeOf(view: View, anchor: string) {
  if (view === 'week') {
    const from = startOfWeek(anchor)
    return { from, to: addDays(from, 7) }
  }
  const from = startOfMonth(anchor)
  const [y, m] = from.split('-').map(Number) as [number, number]
  const to = new Date(Date.UTC(y, m, 1)).toISOString().slice(0, 10)
  return {
    from: view === 'month' ? from : addDays(anchor, -30),
    to: view === 'month' ? to : addDays(anchor, 60),
  }
}

function CancelDialog({
  ctx,
  booking,
  facility,
  onClose,
}: {
  ctx: SiteCtx
  booking: S['Booking']
  facility?: S['Facility']
  onClose: () => void
}) {
  const { t } = useLingui()
  const keys = useKeyStore()
  const [reason, setReason] = useState('')
  const [error, setError] = useState<string | null>(null)
  const refund = facility
    ? refundOnCancel(facility.rules, booking.start)
    : { fee: null, refunded: false }
  const cancel = useOrgMutation(async () => {
    const r = await unwrap(
      gApi.POST('/v1/orgs/{org_id}/bookings/{booking_id}:cancel', {
        params: {
          path: { org_id: ctx.orgId, booking_id: booking.id },
          header: idemHeader(keys.get(booking.id)),
        },
        body: reason.trim() ? { reason: reason.trim() } : {},
      }),
    )
    keys.done(booking.id)
    return r
  })
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>Cancel this booking?</Trans>
          </DialogTitle>
          <DialogDescription>
            {booking.facility_name} ·{' '}
            <SiteDate value={booking.start} timeZone={ctx.timeZone} kind="datetime" />
          </DialogDescription>
        </DialogHeader>
        {refund.fee ? (
          <Callout tone={refund.refunded ? 'success' : 'warning'} role="status">
            {refund.refunded ? (
              <Trans>
                The fee of <Money minor={refund.fee.amount} currency={refund.fee.currency} /> is
                refunded automatically.
              </Trans>
            ) : (
              <Trans>
                It is too late for a refund: the fee of{' '}
                <Money minor={refund.fee.amount} currency={refund.fee.currency} /> is kept.
              </Trans>
            )}
          </Callout>
        ) : null}
        <Field label={t`Reason (optional)`} htmlFor="cb-reason">
          <Input id="cb-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
        <FormRootError message={error ?? undefined} />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            <Trans>Keep the booking</Trans>
          </Button>
          <Button
            variant="destructive"
            disabled={cancel.isPending}
            onClick={() =>
              cancel.mutate(undefined, {
                onSuccess: () => {
                  toast.info(t`The booking was cancelled.`)
                  onClose()
                },
                onError: () => setError(t`Could not cancel the booking.`),
              })
            }
          >
            <Trans>Cancel the booking</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function BlockDialog({
  ctx,
  facilities,
  onClose,
}: {
  ctx: SiteCtx
  facilities: S['Facility'][]
  onClose: () => void
}) {
  const { t } = useLingui()
  const keys = useKeyStore()
  const today = todayIn(ctx.timeZone)
  const [facilityId, setFacilityId] = useState(facilities[0]?.id ?? '')
  const [from, setFrom] = useState(`${today}T09:00`)
  const [to, setTo] = useState(`${today}T22:00`)
  const [reason, setReason] = useState('')
  const [error, setError] = useState<string | null>(null)
  const blocks = useOrgQuery(
    ['blocks', facilityId],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/facilities/{facility_id}/blocks', {
          params: { path: { org_id: ctx.orgId, facility_id: facilityId }, query: { limit: 100 } },
        }),
      ).then((r) => r.items),
    { enabled: facilityId !== '' },
  )
  const add = useOrgMutation(async () =>
    unwrap(
      gApi.POST('/v1/orgs/{org_id}/facilities/{facility_id}/blocks', {
        params: {
          path: { org_id: ctx.orgId, facility_id: facilityId },
          header: idemHeader(keys.get(`${facilityId}${from}${to}`)),
        },
        body: {
          start: localInputToInstant(from, ctx.timeZone),
          end: localInputToInstant(to, ctx.timeZone),
          reason: reason.trim() || null,
        },
      }),
    ),
  )
  const del = useOrgMutation(async (id: string) => {
    await gApi.DELETE('/v1/orgs/{org_id}/facilities/{facility_id}/blocks/{block_id}', {
      params: { path: { org_id: ctx.orgId, facility_id: facilityId, block_id: id } },
    })
  })
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>Block dates</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>Residents cannot book a blocked period (maintenance, private events).</Trans>
          </DialogDescription>
        </DialogHeader>
        <Field label={t`Facility`} htmlFor="bl-fac">
          <Select id="bl-fac" value={facilityId} onChange={(e) => setFacilityId(e.target.value)}>
            {facilities.map((f) => (
              <option key={f.id} value={f.id}>
                {f.name}
              </option>
            ))}
          </Select>
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t`From`} htmlFor="bl-from">
            <Input
              id="bl-from"
              type="datetime-local"
              dir="ltr"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
            />
          </Field>
          <Field label={t`Until`} htmlFor="bl-to">
            <Input
              id="bl-to"
              type="datetime-local"
              dir="ltr"
              value={to}
              onChange={(e) => setTo(e.target.value)}
            />
          </Field>
        </div>
        <Field label={t`Reason`} htmlFor="bl-reason">
          <Input id="bl-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
        <FormRootError message={error ?? undefined} />
        <div>
          <Button
            disabled={!facilityId || to <= from || add.isPending}
            onClick={() =>
              add.mutate(undefined, {
                onSuccess: () => {
                  setError(null)
                  setReason('')
                },
                onError: () =>
                  setError(t`Could not block these dates. Existing bookings may overlap.`),
              })
            }
          >
            <Trans>Block</Trans>
          </Button>
        </div>
        <ul className="flex flex-col gap-1 text-sm" aria-label={t`Blocked periods`}>
          {(blocks.data ?? []).map((b) => (
            <li key={b.id} className="flex items-center justify-between gap-2">
              <span>
                <SiteDate value={b.start} timeZone={ctx.timeZone} kind="datetime" /> –{' '}
                <SiteDate value={b.end} timeZone={ctx.timeZone} kind="datetime" />{' '}
                {b.reason ? `· ${b.reason}` : ''}
              </span>
              <Button size="sm" variant="ghost" onClick={() => del.mutate(b.id)}>
                <Trans>Remove</Trans>
              </Button>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  )
}

/** Week and month calendar plus list; residents see other people's slots only as "taken" (C1). */
export function BookingsPage() {
  const { t } = useLingui()
  const { intl } = useLocale()
  const { can } = useSession()
  const ctx = useSiteCtx()!
  const { orgId } = useParams({ strict: false }) as { orgId: string }
  const manager = can('bookings.manage')
  const names = useWeekdayNames()
  const facilities = useFacilities(ctx)
  const [view, setView] = useState<View>('week')
  const [anchor, setAnchor] = useState(() => todayIn(ctx.timeZone))
  const [facilityId, setFacilityId] = useState('')
  const [booking, setBooking] = useState(false)
  const [blocking, setBlocking] = useState(false)
  const [cancelling, setCancelling] = useState<S['Booking'] | null>(null)
  const range = rangeOf(view, anchor)
  const fromIso = localInputToInstant(`${range.from}T00:00`, ctx.timeZone)
  const toIso = localInputToInstant(`${range.to}T00:00`, ctx.timeZone)
  const list = facilities.data ?? []
  const visible = facilityId ? list.filter((f) => f.id === facilityId) : list

  const bookings = useOrgQuery(['bookings', ctx.siteId, facilityId, range.from, range.to], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/bookings', {
        params: {
          path: { org_id: ctx.orgId },
          query: {
            site_id: ctx.siteId,
            from: fromIso,
            to: toIso,
            limit: 200,
            ...(facilityId ? { facility_id: facilityId } : {}),
            ...(manager ? {} : { mine: true }),
          },
        },
      }),
    ).then((r) => r.items),
  )
  // residents see what is taken, not who took it: availability per facility
  const availability = useQueries({
    queries: manager
      ? []
      : visible.map((f) => ({
          queryKey: okey(orgId, 'availability', f.id, range.from, range.to),
          queryFn: () =>
            unwrap(
              gApi.GET('/v1/orgs/{org_id}/facilities/{facility_id}/availability', {
                params: {
                  path: { org_id: ctx.orgId, facility_id: f.id },
                  query: { from: fromIso, to: toIso },
                },
              }),
            ),
        })),
  })
  const blocksQ = useQueries({
    queries: manager
      ? visible.map((f) => ({
          queryKey: okey(orgId, 'blocks-range', f.id, range.from, range.to),
          queryFn: () =>
            unwrap(
              gApi.GET('/v1/orgs/{org_id}/facilities/{facility_id}/blocks', {
                params: {
                  path: { org_id: ctx.orgId, facility_id: f.id },
                  query: { from: fromIso, to: toIso, limit: 100 },
                },
              }),
            ).then((r) => r.items.map((b) => ({ ...b, facility_name: f.name }))),
        }))
      : [],
  })

  const slots: Slot[] = (() => {
    const out: Slot[] = []
    const own = new Set<string>()
    for (const b of bookings.data ?? []) {
      if (b.status !== 'confirmed') continue
      own.add(`${b.facility_id}|${b.start}`)
      out.push({
        key: b.id,
        start: b.start,
        end: b.end,
        kind: 'booking',
        label: manager ? `${b.facility_name} · ${b.unit_door_number}` : b.facility_name,
        booking: b,
      })
    }
    availability.forEach((q, i) => {
      const f = visible[i]
      for (const s of q.data?.taken ?? []) {
        if (own.has(`${f?.id}|${s.start}`)) continue
        out.push({
          key: `t-${f?.id}-${s.start}`,
          start: s.start,
          end: s.end,
          kind: 'taken',
          label: `${f?.name ?? ''} · ${t`Taken`}`,
        })
      }
      for (const s of q.data?.blocked ?? [])
        out.push({
          key: `b-${f?.id}-${s.start}`,
          start: s.start,
          end: s.end,
          kind: 'blocked',
          label: `${f?.name ?? ''} · ${t`Not available`}`,
        })
    })
    for (const q of blocksQ)
      for (const b of q.data ?? [])
        out.push({
          key: `bl-${b.id}`,
          start: b.start,
          end: b.end,
          kind: 'blocked',
          label: `${b.facility_name} · ${b.reason ?? t`Blocked`}`,
        })
    return out.sort((a, b) => a.start.localeCompare(b.start))
  })()

  const dayOf = (iso: string) => instantToLocalInput(iso, ctx.timeZone).slice(0, 10)
  const time = (iso: string) => instantToLocalInput(iso, ctx.timeZone).slice(11)
  const days: string[] = []
  for (let d = range.from; d < range.to; d = addDays(d, 1)) days.push(d)
  const monthLead = view === 'month' ? (new Date(`${range.from}T12:00:00Z`).getUTCDay() + 6) % 7 : 0
  const shift = (dir: -1 | 1) =>
    setAnchor(
      view === 'week'
        ? addDays(anchor, 7 * dir)
        : view === 'month'
          ? addDays(startOfMonth(anchor), dir === 1 ? 32 : -1)
          : addDays(anchor, 30 * dir),
    )
  const title =
    view === 'week'
      ? `${new Intl.DateTimeFormat(intl, { dateStyle: 'medium', timeZone: 'UTC' }).format(new Date(`${range.from}T12:00:00Z`))} – ${new Intl.DateTimeFormat(intl, { dateStyle: 'medium', timeZone: 'UTC' }).format(new Date(`${addDays(range.to, -1)}T12:00:00Z`))}`
      : new Intl.DateTimeFormat(intl, { month: 'long', year: 'numeric', timeZone: 'UTC' }).format(
          new Date(`${range.from}T12:00:00Z`),
        )

  const chip = (s: Slot) => (
    <li
      key={s.key}
      className={cn(
        'flex flex-col rounded px-1.5 py-1 text-xs',
        s.kind === 'booking' && 'bg-primary/10',
        s.kind === 'taken' && 'bg-muted text-muted-foreground',
        s.kind === 'blocked' && 'bg-warning/15',
      )}
    >
      <span className="font-medium tabular-nums">
        {time(s.start)}–{time(s.end)}
      </span>
      <span className="truncate">{s.label}</span>
      {s.booking && (manager || can('bookings.create')) ? (
        <button
          type="button"
          className="w-fit text-start underline-offset-2 hover:underline"
          onClick={() => setCancelling(s.booking!)}
        >
          <Trans>Cancel</Trans>
        </button>
      ) : null}
    </li>
  )

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Bookings`}
        description={t`Reserve halls, courts and barbecue areas. Bookings inside the rules are confirmed at once.`}
        actions={
          <>
            {can('facilities.manage') ? (
              <Link
                to="/orgs/$orgId/facilities"
                params={{ orgId: ctx.orgId }}
                className={buttonVariants({ variant: 'outline' })}
              >
                <Trans>Facilities and rules</Trans>
              </Link>
            ) : null}
            {can('facilities.manage') && list.length > 0 ? (
              <Button variant="outline" onClick={() => setBlocking(true)}>
                <Trans>Block dates</Trans>
              </Button>
            ) : null}
            {can('bookings.create') ? (
              <Button disabled={list.length === 0} onClick={() => setBooking(true)}>
                <PlusIcon />
                <Trans>Book</Trans>
              </Button>
            ) : null}
          </>
        }
      />
      <div className="flex flex-wrap items-center gap-2">
        <Select
          aria-label={t`Facility`}
          className="w-auto"
          value={facilityId}
          onChange={(e) => setFacilityId(e.target.value)}
        >
          <option value="">{t`All facilities`}</option>
          {list.map((f) => (
            <option key={f.id} value={f.id}>
              {f.name}
            </option>
          ))}
        </Select>
        <div role="group" aria-label={t`View`} className="inline-flex rounded-md border p-0.5">
          {(['week', 'month', 'list'] as const).map((v) => (
            <button
              key={v}
              type="button"
              aria-pressed={view === v}
              onClick={() => setView(v)}
              className={cn(
                'rounded px-3 py-1 text-sm',
                view === v ? 'bg-primary text-primary-foreground' : 'hover:bg-accent',
              )}
            >
              {v === 'week' ? t`Week` : v === 'month' ? t`Month` : t`List`}
            </button>
          ))}
        </div>
        <div className="ms-auto flex items-center gap-1">
          <Button variant="outline" size="icon" aria-label={t`Previous`} onClick={() => shift(-1)}>
            <ChevronLeftIcon className="rtl-flip" />
          </Button>
          <span className="min-w-40 text-center text-sm font-medium" aria-live="polite">
            {view === 'list' ? t`Upcoming and recent` : title}
          </span>
          <Button variant="outline" size="icon" aria-label={t`Next`} onClick={() => shift(1)}>
            <ChevronRightIcon className="rtl-flip" />
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setAnchor(todayIn(ctx.timeZone))}>
            <Trans>Today</Trans>
          </Button>
        </div>
      </div>
      {!manager ? (
        <Callout tone="info">
          <Trans>Slots booked by others are shown only as "taken".</Trans>
        </Callout>
      ) : null}
      <QueryBoundary query={bookings} skeleton="page">
        {() =>
          view === 'list' ? (
            slots.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                <Trans>No bookings in this period.</Trans>
              </p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>
                      <Trans>When</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Facility</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Booked by</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Fee</Trans>
                    </TableHead>
                    <TableHead>
                      <span className="sr-only">
                        <Trans>Actions</Trans>
                      </span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {slots.map((s) => (
                    <TableRow key={s.key}>
                      <TableCell>
                        <SiteDate value={s.start} timeZone={ctx.timeZone} kind="datetime" /> –{' '}
                        {time(s.end)}
                      </TableCell>
                      <TableCell>{s.booking?.facility_name ?? s.label}</TableCell>
                      <TableCell>
                        {s.booking ? (
                          manager ? (
                            `${s.booking.unit_door_number} · ${s.booking.person_display_name ?? ''}`
                          ) : (
                            t`My unit`
                          )
                        ) : (
                          <Badge variant="secondary">
                            {s.kind === 'blocked' ? t`Blocked` : t`Taken`}
                          </Badge>
                        )}
                      </TableCell>
                      <TableCell>
                        {s.booking?.fee ? (
                          <Money minor={s.booking.fee.amount} currency={s.booking.fee.currency} />
                        ) : (
                          '–'
                        )}
                      </TableCell>
                      <TableCell>
                        {s.booking ? (
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => setCancelling(s.booking!)}
                          >
                            <Trans>Cancel</Trans>
                          </Button>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )
          ) : (
            <div
              className={cn(
                'grid gap-px overflow-hidden rounded-md border bg-border',
                'grid-cols-7',
              )}
              role="group"
              aria-label={t`Calendar`}
            >
              {names.map((n) => (
                <div
                  key={n}
                  aria-hidden="true"
                  className="bg-muted px-2 py-1 text-center text-xs font-medium"
                >
                  {n}
                </div>
              ))}
              {Array.from({ length: monthLead }, (_, i) => (
                <div key={`lead-${i}`} className="bg-background" aria-hidden="true" />
              ))}
              {days.map((d) => (
                <div
                  key={d}
                  role="group"
                  aria-label={d}
                  className={cn(
                    'flex min-h-14 sm:min-h-24 flex-col gap-1 bg-background p-1',
                    d === todayIn(ctx.timeZone) && 'ring-2 ring-inset ring-primary',
                  )}
                >
                  <span className="text-xs text-muted-foreground tabular-nums">
                    {Number(d.slice(8))}
                  </span>
                  <ul className="flex flex-col gap-1">
                    {slots.filter((s) => dayOf(s.start) === d).map(chip)}
                  </ul>
                </div>
              ))}
            </div>
          )
        }
      </QueryBoundary>
      {booking ? (
        <BookDialog
          ctx={ctx}
          facilities={list}
          bookings={bookings.data ?? []}
          initialFacilityId={facilityId}
          initialDate={anchor}
          onClose={() => setBooking(false)}
        />
      ) : null}
      {blocking ? (
        <BlockDialog ctx={ctx} facilities={list} onClose={() => setBlocking(false)} />
      ) : null}
      {cancelling ? (
        <CancelDialog
          ctx={ctx}
          booking={cancelling}
          facility={list.find((f) => f.id === cancelling.facility_id)}
          onClose={() => setCancelling(null)}
        />
      ) : null}
    </div>
  )
}
