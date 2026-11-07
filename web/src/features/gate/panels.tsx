import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2Icon, DoorOpenIcon, LogOutIcon, PackageIcon, PhoneIcon, SearchIcon, XCircleIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { useSession } from '@/app/session'
import { useUnits } from '@/features/meetings/hooks'
import { okey, unwrap, useKeyStore, type S, type SiteCtx } from '@/features/meetings/kit/api'
import { QrScanner } from '@/features/meetings/kit/QrScanner'
import { Field, Select } from '@/features/meetings/kit/ui'
import { Callout } from '@/features/meetings/kit/widgets'
import { guardApi } from './guardApi'
import { manualOpenState, type OpenBlock } from './openState'

const big = 'h-14 text-lg'
const POLL = 10_000

const key = (ctx: SiteCtx, ...rest: unknown[]) => okey(ctx.orgId, 'gate', ctx.siteId, ...rest)
const idem = (k: string) => ({ 'Idempotency-Key': k })

/** Reload everything the console shows after any action. */
export function useRefresh(ctx: SiteCtx) {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: okey(ctx.orgId, 'gate', ctx.siteId) })
}

export function useConsole(ctx: SiteCtx) {
  return useQuery({
    queryKey: key(ctx, 'console'),
    queryFn: () =>
      unwrap(
        guardApi.GET('/v1/orgs/{org_id}/gate/console', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
        }),
      ),
    refetchInterval: POLL,
  })
}

const reasonText = (t: ReturnType<typeof useLingui>['t']) => ({
  match: t`Valid`,
  unknown: t`This code is not known.`,
  outside_window: t`This pass is not valid at this time.`,
  used_up: t`This pass was already used.`,
  revoked: t`This pass was cancelled or revoked.`,
  wrong_site: t`This pass is for another site.`,
})

function approvalBadge(t: ReturnType<typeof useLingui>['t'], s: S['GuardVisitor']['approval_status']) {
  switch (s) {
    case 'pre_approved':
      return <Badge variant="info">{t`Expected`}</Badge>
    case 'approved':
      return <Badge variant="success">{t`Approved`}</Badge>
    case 'requested':
      return <Badge variant="warning">{t`Waiting for the resident`}</Badge>
    case 'denied':
      return <Badge variant="destructive">{t`Denied`}</Badge>
    default:
      return <Badge variant="secondary">{t`Expired`}</Badge>
  }
}

/** Admit (arrive) and mark-left actions of one visitor row. */
export function VisitorActions({ ctx, v, onDone }: { ctx: SiteCtx; v: S['GuardVisitor']; onDone?: () => void }) {
  const { t } = useLingui()
  const refresh = useRefresh(ctx)
  const keys = useKeyStore()
  const [busy, setBusy] = useState(false)
  async function act(kind: 'arrive' | 'leave') {
    setBusy(true)
    const id = `${v.id}-${kind}`
    try {
      if (kind === 'arrive')
        await unwrap(
          guardApi.POST('/v1/orgs/{org_id}/gate/visitors/{entry_id}:arrive', {
            params: { path: { org_id: ctx.orgId, entry_id: v.id }, header: idem(keys.get(id)) },
            body: {},
          }),
        )
      else
        await unwrap(
          guardApi.POST('/v1/orgs/{org_id}/gate/visitors/{entry_id}:leave', {
            params: { path: { org_id: ctx.orgId, entry_id: v.id }, header: idem(keys.get(id)) },
          }),
        )
      keys.done(id)
      toast.info(kind === 'arrive' ? t`${v.name} admitted.` : t`${v.name} marked as left.`)
      await refresh()
      onDone?.()
    } catch {
      toast.error(t`That did not work. Check the visitor's status.`)
    } finally {
      setBusy(false)
    }
  }
  const canAdmit = !v.arrived_at && (v.approval_status === 'pre_approved' || v.approval_status === 'approved')
  if (v.left_at) return <Badge variant="secondary">{t`Left`}</Badge>
  if (v.arrived_at)
    return (
      <Button variant="outline" className={big} disabled={busy} onClick={() => void act('leave')}>
        <LogOutIcon />
        <Trans>Left</Trans>
      </Button>
    )
  if (canAdmit)
    return (
      <Button className={big} disabled={busy} onClick={() => void act('arrive')}>
        <CheckCircle2Icon />
        <Trans>Admit</Trans>
      </Button>
    )
  return null
}

function VisitorRow({ ctx, v }: { ctx: SiteCtx; v: S['GuardVisitor'] }) {
  const { t } = useLingui()
  return (
    <li className="flex flex-wrap items-center gap-3 rounded-lg border p-3">
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="text-lg font-semibold">{v.name}</span>
        <span className="text-sm text-muted-foreground">
          <span className="font-medium text-foreground">{v.unit_door_number}</span>
          {v.plate ? <span dir="ltr"> · {v.plate}</span> : null}
          {' · '}
          <SiteDate value={v.window_from} timeZone={ctx.timeZone} kind="time" />–
          <SiteDate value={v.window_to} timeZone={ctx.timeZone} kind="time" />
          {v.recurring ? ` · ${t`Recurring`}` : ''}
        </span>
      </div>
      {v.arrived_at && !v.left_at ? <Badge variant="success">{t`On site`}</Badge> : approvalBadge(t, v.approval_status)}
      <VisitorActions ctx={ctx} v={v} />
    </li>
  )
}

/** Today's expected visitors, refreshed every few seconds with the console. */
export function ExpectedList({ ctx, visitors }: { ctx: SiteCtx; visitors: S['GuardVisitor'][] }) {
  const { t } = useLingui()
  return (
    <section aria-labelledby="expected-h" className="flex flex-col gap-3">
      <h2 id="expected-h" className="text-2xl font-semibold">
        <Trans>Expected today</Trans>
      </h2>
      {visitors.length === 0 ? (
        <p className="rounded-lg border border-dashed p-6 text-lg text-muted-foreground">{t`Nobody is expected right now.`}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {visitors.map((v) => (
            <VisitorRow key={v.id} ctx={ctx} v={v} />
          ))}
        </ul>
      )}
    </section>
  )
}

/** Search by name or plate (minimized: the guard sees name, plate, unit, window, nothing else). */
export function SearchPanel({ ctx }: { ctx: SiteCtx }) {
  const { t } = useLingui()
  const [q, setQ] = useState('')
  const term = q.trim()
  const visitors = useQuery({
    queryKey: key(ctx, 'search', term),
    enabled: term.length >= 2,
    queryFn: () =>
      unwrap(
        guardApi.GET('/v1/orgs/{org_id}/gate/visitors', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, q: term, limit: 20 } },
        }),
      ).then((r) => r.items),
  })
  const plates = useQuery({
    queryKey: key(ctx, 'plates', term),
    enabled: term.length >= 2,
    queryFn: () =>
      unwrap(
        guardApi.GET('/v1/orgs/{org_id}/gate/plates', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, q: term, limit: 10 } },
        }),
      ).then((r) => r.items),
  })
  return (
    <section aria-labelledby="search-h" className="flex flex-col gap-3">
      <h2 id="search-h" className="text-2xl font-semibold">
        <Trans>Search</Trans>
      </h2>
      <div className="relative">
        <SearchIcon className="pointer-events-none absolute start-3 top-4 size-5 text-muted-foreground" aria-hidden="true" />
        <Input
          aria-label={t`Search by name or plate`}
          placeholder={t`Name or plate`}
          className="h-14 ps-11 text-lg"
          autoComplete="off"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
      </div>
      {term.length >= 2 ? (
        <>
          {(plates.data ?? []).length > 0 ? (
            <ul className="flex flex-col gap-1" aria-label={t`Resident vehicles`}>
              {(plates.data ?? []).map((p) => (
                <li key={p.plate} className="rounded-lg bg-muted px-3 py-2 text-lg">
                  <span dir="ltr" className="font-semibold">
                    {p.plate}
                  </span>{' '}
                  <Trans>resident vehicle, unit {p.unit_door_number}</Trans>
                </li>
              ))}
            </ul>
          ) : null}
          {visitors.isPending ? (
            <p role="status" className="text-muted-foreground">
              <Trans>Searching…</Trans>
            </p>
          ) : visitors.isError ? (
            <p role="alert" className="text-destructive">
              <Trans>The search failed.</Trans>
            </p>
          ) : visitors.data.length === 0 && (plates.data ?? []).length === 0 ? (
            <p className="text-lg text-muted-foreground">
              <Trans>No match. Use "Walk-in visitor" to ask the resident.</Trans>
            </p>
          ) : (
            <ul className="flex flex-col gap-2">
              {visitors.data.map((v) => (
                <VisitorRow key={v.id} ctx={ctx} v={v} />
              ))}
            </ul>
          )}
        </>
      ) : null}
    </section>
  )
}

/** Scan a visitor's QR (camera or typed) and admit directly when it is valid for now. */
export function ScanPanel({ ctx }: { ctx: SiteCtx }) {
  const { t } = useLingui()
  const keys = useKeyStore()
  const [result, setResult] = useState<S['ScanResult'] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const refresh = useRefresh(ctx)
  const reasons = reasonText(t)
  async function scan(token: string) {
    setBusy(true)
    setError(null)
    setResult(null)
    const id = `scan-${token}`
    try {
      const r = await unwrap(
        guardApi.POST('/v1/orgs/{org_id}/gate/visitors:scan', {
          params: { path: { org_id: ctx.orgId }, header: idem(keys.get(id)) },
          body: { token },
        }),
      )
      keys.done(id)
      setResult(r)
    } catch {
      keys.done(id)
      setError(t`Could not check the code. Try again.`)
    } finally {
      setBusy(false)
    }
  }
  return (
    <section aria-labelledby="scan-h" className="flex flex-col gap-3">
      <h2 id="scan-h" className="text-2xl font-semibold">
        <Trans>Scan a pass</Trans>
      </h2>
      <QrScanner onScan={(tok) => void scan(tok)} busy={busy} large />
      {error ? (
        <p role="alert" className="text-lg text-destructive">
          {error}
        </p>
      ) : null}
      {result ? (
        <div
          role="status"
          className={cn('flex flex-col gap-3 rounded-lg border-2 p-4', result.valid ? 'border-success bg-success/10' : 'border-destructive bg-destructive/10')}
        >
          <div className="flex items-center gap-2 text-xl font-semibold">
            {result.valid ? <CheckCircle2Icon className="size-7 text-success" /> : <XCircleIcon className="size-7 text-destructive" />}
            {result.valid ? t`Valid pass` : reasons[result.reason]}
          </div>
          {result.entry ? (
            <div className="text-lg">
              <span className="font-semibold">{result.entry.name}</span> · {result.entry.unit_door_number}
              {result.entry.plate ? <span dir="ltr"> · {result.entry.plate}</span> : null}
            </div>
          ) : null}
          {result.valid && result.entry ? (
            <VisitorActions
              ctx={ctx}
              v={result.entry}
              onDone={() => {
                setResult(null)
                void refresh()
              }}
            />
          ) : null}
        </div>
      ) : null}
    </section>
  )
}

/** Unexpected visitor: ask the resident (approve or deny), watch the answer, call if nobody answers. */
export function WalkInPanel({ ctx }: { ctx: SiteCtx }) {
  const { t } = useLingui()
  const idemKey = useIdempotencyKey()
  const units = useUnits(ctx.orgId, ctx.siteId)
  const [name, setName] = useState('')
  const [plate, setPlate] = useState('')
  const [unitId, setUnitId] = useState('')
  const [entryId, setEntryId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const refresh = useRefresh(ctx)

  const entry = useQuery({
    queryKey: key(ctx, 'walkin', entryId),
    enabled: entryId !== null,
    queryFn: () =>
      unwrap(
        guardApi.GET('/v1/orgs/{org_id}/gate/visitors/{entry_id}', {
          params: { path: { org_id: ctx.orgId, entry_id: entryId! } },
        }),
      ),
    refetchInterval: (q) => (q.state.data?.approval_status === 'requested' ? 3000 : false),
  })
  const [showCall, setShowCall] = useState(false)
  const call = useQuery({
    queryKey: key(ctx, 'call', entryId),
    enabled: showCall && entryId !== null,
    queryFn: () =>
      unwrap(
        guardApi.GET('/v1/orgs/{org_id}/gate/visitors/{entry_id}/call-contact', {
          params: { path: { org_id: ctx.orgId, entry_id: entryId! } },
        }),
      ),
  })

  async function ask() {
    if (!name.trim() || !unitId) return
    setBusy(true)
    setError(null)
    try {
      const e = await unwrap(
        guardApi.POST('/v1/orgs/{org_id}/gate/visitors:walk-in', {
          params: { path: { org_id: ctx.orgId }, header: idem(idemKey.key) },
          body: { site_id: ctx.siteId, unit_id: unitId, name: name.trim(), ...(plate.trim() ? { plate: plate.trim() } : {}) },
        }),
      )
      idemKey.renew()
      setEntryId(e.id)
      setShowCall(false)
      void refresh()
    } catch (e) {
      setError(
        e instanceof AppError && e.code === 'sms_unavailable'
          ? t`The request could not be sent by SMS. Call the resident instead.`
          : t`Could not send the request.`,
      )
    } finally {
      setBusy(false)
    }
  }

  const status = entry.data?.approval_status
  const reset = () => {
    setEntryId(null)
    setName('')
    setPlate('')
    setShowCall(false)
  }
  return (
    <section aria-labelledby="walkin-h" className="flex flex-col gap-3">
      <h2 id="walkin-h" className="text-2xl font-semibold">
        <Trans>Walk-in visitor</Trans>
      </h2>
      {entryId === null ? (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            void ask()
          }}
        >
          <Field label={t`Visitor's name`} htmlFor="wi-name">
            <Input id="wi-name" className="h-14 text-lg" autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label={t`Plate (optional)`} htmlFor="wi-plate">
            <Input id="wi-plate" dir="ltr" className="h-14 text-lg" autoComplete="off" value={plate} onChange={(e) => setPlate(e.target.value)} />
          </Field>
          <Field label={t`Visiting unit`} htmlFor="wi-unit">
            <Select id="wi-unit" className="h-14 text-lg" value={unitId} onChange={(e) => setUnitId(e.target.value)}>
              <option value="">{t`Choose the unit`}</option>
              {(units.data ?? []).map((u) => (
                <option key={u.id} value={u.id}>
                  {u.door_number}
                </option>
              ))}
            </Select>
          </Field>
          {units.isError ? (
            <p role="alert" className="text-sm text-destructive">
              <Trans>The list of units is not available to this account.</Trans>
            </p>
          ) : null}
          {error ? (
            <p role="alert" className="text-destructive">
              {error}
            </p>
          ) : null}
          <Button type="submit" className="h-14 text-lg" disabled={busy || !name.trim() || !unitId}>
            <Trans>Ask the resident</Trans>
          </Button>
        </form>
      ) : (
        <div className="flex flex-col gap-3 rounded-lg border p-4" role="status" aria-live="polite">
          <div className="text-xl font-semibold">{entry.data?.name ?? name}</div>
          {status === 'requested' || status === undefined ? (
            <>
              <Callout tone="warning">
                <Trans>Waiting for {entry.data?.unit_door_number ?? ''} to answer. This checks every few seconds.</Trans>
              </Callout>
              <Button variant="outline" className={big} onClick={() => setShowCall(true)}>
                <PhoneIcon />
                <Trans>Call the resident</Trans>
              </Button>
            </>
          ) : status === 'approved' && entry.data ? (
            <>
              <Callout tone="success">
                <Trans>Approved by the resident.</Trans>
              </Callout>
              <VisitorActions ctx={ctx} v={entry.data} onDone={reset} />
            </>
          ) : status === 'denied' ? (
            <Callout tone="destructive">
              <Trans>The resident denied this visitor. Do not admit.</Trans>
            </Callout>
          ) : (
            <>
              <Callout tone="warning">
                <Trans>No answer in time. Call the resident.</Trans>
              </Callout>
              <Button variant="outline" className={big} onClick={() => setShowCall(true)}>
                <PhoneIcon />
                <Trans>Call the resident</Trans>
              </Button>
            </>
          )}
          {showCall ? (
            call.isPending ? (
              <p role="status">
                <Trans>Looking up the number…</Trans>
              </p>
            ) : call.isError ? (
              <p role="alert" className="text-destructive">
                <Trans>This resident did not share a number for the gate.</Trans>
              </p>
            ) : (
              <a href={`tel:${call.data.phone}`} className="text-2xl font-semibold underline" dir="ltr">
                {call.data.display_name ? `${call.data.display_name} · ` : ''}
                {call.data.phone}
              </a>
            )
          ) : null}
          <Button variant="ghost" className={big} onClick={reset}>
            <Trans>New visitor</Trans>
          </Button>
        </div>
      )}
    </section>
  )
}

/** Parcels (C3): log against a unit, mark collected by whom. */
export function ParcelsPanel({ ctx, waiting }: { ctx: SiteCtx; waiting: S['GateDelivery'][] }) {
  const { t } = useLingui()
  const units = useUnits(ctx.orgId, ctx.siteId)
  const keys = useKeyStore()
  const logKey = useIdempotencyKey()
  const refresh = useRefresh(ctx)
  const [unitId, setUnitId] = useState('')
  const [desc, setDesc] = useState('')
  const [collector, setCollector] = useState<Record<string, string>>({})
  async function log() {
    try {
      await unwrap(
        guardApi.POST('/v1/orgs/{org_id}/gate/deliveries', {
          params: { path: { org_id: ctx.orgId }, header: idem(logKey.key) },
          body: { site_id: ctx.siteId, unit_id: unitId, ...(desc.trim() ? { description: desc.trim() } : {}) },
        }),
      )
      logKey.renew()
      setDesc('')
      toast.info(t`Parcel logged. The resident is notified.`)
      await refresh()
    } catch {
      toast.error(t`Could not log the parcel.`)
    }
  }
  async function collect(d: S['GateDelivery']) {
    const who = collector[d.id]?.trim()
    if (!who) return
    const id = `collect-${d.id}`
    try {
      await unwrap(
        guardApi.POST('/v1/orgs/{org_id}/gate/deliveries/{delivery_id}:collect', {
          params: { path: { org_id: ctx.orgId, delivery_id: d.id }, header: idem(keys.get(id)) },
          body: { collected_by_name: who },
        }),
      )
      keys.done(id)
      await refresh()
    } catch {
      toast.error(t`Could not mark the parcel as collected.`)
    }
  }
  return (
    <section aria-labelledby="parcels-h" className="flex flex-col gap-3">
      <h2 id="parcels-h" className="flex items-center gap-2 text-2xl font-semibold">
        <PackageIcon className="size-6" aria-hidden="true" />
        <Trans>Parcels</Trans>
      </h2>
      <div className="grid gap-2 sm:grid-cols-[8rem_1fr_auto]">
        <Select aria-label={t`Unit`} className="h-14 text-lg" value={unitId} onChange={(e) => setUnitId(e.target.value)}>
          <option value="">{t`Unit`}</option>
          {(units.data ?? []).map((u) => (
            <option key={u.id} value={u.id}>
              {u.door_number}
            </option>
          ))}
        </Select>
        <Input aria-label={t`Description (optional)`} placeholder={t`What is it? (optional)`} className="h-14 text-lg" value={desc} onChange={(e) => setDesc(e.target.value)} />
        <Button className={big} disabled={!unitId} onClick={() => void log()}>
          <Trans>Log parcel</Trans>
        </Button>
      </div>
      {waiting.length === 0 ? (
        <p className="text-lg text-muted-foreground">
          <Trans>No parcels are waiting.</Trans>
        </p>
      ) : (
        <ul className="flex flex-col gap-2">
          {waiting.map((d) => (
            <li key={d.id} className="flex flex-wrap items-center gap-2 rounded-lg border p-3">
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="text-lg font-semibold">{d.unit_door_number}</span>
                <span className="text-sm text-muted-foreground">
                  {d.description ?? t`Parcel`} · <SiteDate value={d.received_at} timeZone={ctx.timeZone} kind="datetime" />
                </span>
              </div>
              <Input
                aria-label={t`Collected by ${d.unit_door_number}`}
                placeholder={t`Collected by`}
                className="h-14 max-w-48 text-lg"
                value={collector[d.id] ?? ''}
                onChange={(e) => setCollector((c) => ({ ...c, [d.id]: e.target.value }))}
              />
              <Button className={big} disabled={!(collector[d.id] ?? '').trim()} onClick={() => void collect(d)}>
                <Trans>Collected</Trans>
              </Button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

const blockText = (t: ReturnType<typeof useLingui>['t'], r: OpenBlock) =>
  ({
    no_permission: t`You may not open gates.`,
    point_disabled: t`This gate is switched off.`,
    agent_offline: t`The gate agent is offline.`,
    clock_suspect: t`The gate agent's clock is wrong; it will not act.`,
  })[r]

/** One manual open button: disabled with the reason when it cannot work. */
export function OpenButton({
  ctx,
  point,
  console: c,
  canOpen,
  clockSuspect,
}: {
  ctx: SiteCtx
  point: S['GatePoint']
  console: Pick<S['GateConsole'], 'agent' | 'server_time'>
  canOpen: boolean
  clockSuspect?: boolean
}) {
  const { t } = useLingui()
  const refresh = useRefresh(ctx)
  const k = useIdempotencyKey()
  const [phase, setPhase] = useState<'idle' | 'sending' | 'done' | 'failed'>('idle')
  const [learned, setLearned] = useState<OpenBlock | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => void (timer.current && clearTimeout(timer.current)), [])
  const state = manualOpenState({ canOpen, point, agent: c.agent, serverTime: c.server_time, clockSuspect })
  const reason = state.reason ?? learned
  async function open() {
    setPhase('sending')
    try {
      const cmd = await unwrap(
        guardApi.POST('/v1/orgs/{org_id}/access-points/{point_id}:open', {
          params: { path: { org_id: ctx.orgId, point_id: point.id }, header: idem(k.key) },
          body: {},
        }),
      )
      // the command lives 15 seconds: poll until the agent reports
      let status = cmd.status
      for (let i = 0; i < 12 && (status === 'pending' || status === 'delivered'); i++) {
        await new Promise((r) => setTimeout(r, 1000))
        const c2 = await unwrap(
          guardApi.GET('/v1/orgs/{org_id}/access/commands/{command_id}', {
            params: { path: { org_id: ctx.orgId, command_id: cmd.id } },
          }),
        )
        status = c2.status
      }
      k.renew()
      setPhase(status === 'done' ? 'done' : 'failed')
      void refresh()
    } catch (e) {
      k.renew()
      if (e instanceof AppError && e.code === 'agent_offline') setLearned('agent_offline')
      if (e instanceof AppError && e.code === 'clock_suspect') setLearned('clock_suspect')
      setPhase('failed')
    }
    timer.current = setTimeout(() => setPhase('idle'), 4000)
  }
  return (
    <div className="flex flex-col gap-1">
      <Button
        className="h-20 w-full justify-start gap-3 text-xl"
        disabled={reason !== null || phase === 'sending'}
        onClick={() => void open()}
        aria-describedby={reason ? `why-${point.id}` : undefined}
      >
        <DoorOpenIcon className="size-7" aria-hidden="true" />
        {phase === 'sending' ? t`Opening…` : phase === 'done' ? t`Opened` : t`Open ${point.name}`}
      </Button>
      {reason ? (
        <p id={`why-${point.id}`} className="text-sm text-muted-foreground">
          {blockText(t, reason)}
        </p>
      ) : null}
      {phase === 'failed' && !reason ? (
        <p role="alert" className="text-sm text-destructive">
          {t`The gate did not confirm. Try again or check the gate.`}
        </p>
      ) : null}
    </div>
  )
}

/** Manual open per access point plus the most recent openings. */
export function OpenPanel({ ctx, console: c }: { ctx: SiteCtx; console: S['GateConsole'] }) {
  const { t } = useLingui()
  const { can } = useSession()
  const events = useQuery({
    queryKey: key(ctx, 'events'),
    queryFn: () =>
      unwrap(
        guardApi.GET('/v1/orgs/{org_id}/gate/events', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 10 } },
        }),
      ).then((r) => r.items),
    refetchInterval: 5000,
  })
  const points = c.agent?.points ?? []
  const decisionLabel = { allow: t`Opened`, deny: t`Refused`, manual_open: t`Opened by hand` }
  return (
    <section aria-labelledby="open-h" className="flex flex-col gap-3">
      <h2 id="open-h" className="text-2xl font-semibold">
        <Trans>Gates</Trans>
      </h2>
      {points.length === 0 ? (
        <p className="text-lg text-muted-foreground">
          <Trans>No gate is connected to this console.</Trans>
        </p>
      ) : (
        points.map((p) => <OpenButton key={p.id} ctx={ctx} point={p} console={c} canOpen={can('access.open')} />)
      )}
      <h3 className="mt-2 text-lg font-semibold">
        <Trans>Recent openings</Trans>
      </h3>
      {events.isError ? (
        <p role="alert" className="text-destructive">
          <Trans>The opening log is not available.</Trans>
        </p>
      ) : (events.data ?? []).length === 0 ? (
        <p className="text-muted-foreground">
          <Trans>Nothing yet.</Trans>
        </p>
      ) : (
        <ul className="flex flex-col gap-1" aria-label={t`Recent openings`}>
          {(events.data ?? []).map((e) => (
            <li key={e.sync_seq} className="flex flex-wrap items-center gap-2 rounded-md bg-muted/50 px-3 py-2">
              <SiteDate value={e.occurred_at} timeZone={ctx.timeZone} kind="time" className="tabular-nums" />
              <Badge variant={e.decision === 'deny' ? 'destructive' : 'success'}>{decisionLabel[e.decision] ?? e.decision}</Badge>
              <span className="min-w-0 flex-1 truncate">
                {e.point.name}
                {e.plate ? ` · ${e.plate}` : ''}
                {e.who.unit_label ? ` · ${e.who.unit_label}` : ''}
                {e.who.visitor_name ? ` · ${e.who.visitor_name}` : ''}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
