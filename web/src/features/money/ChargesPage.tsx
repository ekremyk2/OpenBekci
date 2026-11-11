import { useMemo, useState } from 'react'
import { useLingui, Trans } from '@lingui/react/macro'
import { useIdempotencyKey } from '@/lib/idempotency'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { MoneyInput } from '@/components/forms/MoneyInput'
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
  TableFooter,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toast } from '@/lib/toast'
import { AppError } from '@/api/errors'
import {
  idemHeader,
  minorMoney,
  moneyApi,
  monthOf,
  todayIn,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
  unitSideRef,
} from './api'
import { CHARGE_TYPES, useLabels } from './labels'
import { UnitPicker } from './pickers'
import {
  LabeledCheckbox,
  Field,
  FormRootError,
  Gate,
  MonthPicker,
  PageHeader,
  QueryBoundary,
  ReasonDialog,
  Select,
  SideBadge,
  StatusBadge,
} from './ui'

export function ChargesPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const labels = useLabels()
  const [period, setPeriod] = useState(monthOf(todayIn(ctx.timeZone)))
  const [unitId, setUnitId] = useState('')
  const [side, setSide] = useState('')
  const [status, setStatus] = useState('')
  const [type, setType] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [cancelFor, setCancelFor] = useState<S['Charge'] | null>(null)
  const [remindOpen, setRemindOpen] = useState(false)
  const filters = { period, unitId, side, status, type }
  const q = useMoneyQuery(['charges', ctx.siteId, filters], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/charges', {
        params: {
          path: { org_id: ctx.orgId },
          query: {
            site_id: ctx.siteId,
            period: period || undefined,
            unit_id: unitId || undefined,
            debtor_kind: side || undefined,
            status: status || undefined,
            charge_type: type || undefined,
            limit: 200,
          },
        },
      }),
    ),
  )
  const toggle = (id: string) =>
    setSelected((s) => {
      const n = new Set(s)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Charges`}
        description={t`Every receivable of a unit side. Charges are never edited: cancel with a reason and a reversal is posted.`}
        actions={
          <Gate anyOf={['collections.manage']} fallback={null}>
            <Button variant="outline" onClick={() => setRemindOpen(true)}>
              <Trans>Send reminders</Trans>
            </Button>
          </Gate>
        }
      />
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <Field label={t`Period`} htmlFor="f-period">
          <MonthPicker id="f-period" value={period} onChange={setPeriod} />
        </Field>
        <Field label={t`Unit`} htmlFor="f-unit">
          <UnitPicker
            ctx={ctx}
            id="f-unit"
            value={unitId}
            onChange={setUnitId}
            placeholder={t`All units`}
          />
        </Field>
        <Field label={t`Side`} htmlFor="f-side">
          <Select id="f-side" value={side} onChange={(e) => setSide(e.target.value)}>
            <option value="">{t`Both`}</option>
            <option value="owner">{t`Owner`}</option>
            <option value="tenant">{t`Tenant`}</option>
          </Select>
        </Field>
        <Field label={t`Status`} htmlFor="f-status">
          <Select id="f-status" value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="">{t`Any`}</option>
            <option value="open">{t`Open`}</option>
            <option value="partially_paid">{t`Partially paid`}</option>
            <option value="paid">{t`Paid`}</option>
            <option value="cancelled">{t`Cancelled`}</option>
          </Select>
        </Field>
        <Field label={t`Type`} htmlFor="f-type">
          <Select id="f-type" value={type} onChange={(e) => setType(e.target.value)}>
            <option value="">{t`Any`}</option>
            {CHARGE_TYPES.map((c) => (
              <option key={c} value={c}>
                {labels.chargeType[c]}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      <QueryBoundary
        query={q}
        isEmpty={(d) => d.items.length === 0}
        emptyTitle={t`No charges match`}
        emptyDescription={t`Change the filters, or create a dues plan to generate charges.`}
      >
        {(data) => {
          const live = data.items.filter((c) => c.status !== 'cancelled')
          const sum = (f: (c: S['Charge']) => number) => live.reduce((a, c) => a + f(c), 0)
          return (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8" />
                    <TableHead>
                      <Trans>Unit</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Side</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Person</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Description</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Due</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Amount</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Paid</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Balance</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Status</Trans>
                    </TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.items.map((c) => (
                    <TableRow
                      key={c.id}
                      className={c.status === 'cancelled' ? 'text-muted-foreground' : undefined}
                    >
                      <TableCell>
                        <input
                          type="checkbox"
                          className="size-4 accent-primary"
                          aria-label={`${t`Select`} ${c.unit_door_number}`}
                          checked={selected.has(`${c.unit_id}:${c.debtor_kind}`)}
                          onChange={() => toggle(`${c.unit_id}:${c.debtor_kind}`)}
                        />
                      </TableCell>
                      <TableCell>{c.unit_door_number}</TableCell>
                      <TableCell>
                        <SideBadge side={c.debtor_kind} />
                      </TableCell>
                      <TableCell>
                        {c.effective_debtor.display_name ?? c.debtor_display_name ?? '–'}
                        {c.effective_debtor.source === 'carried' ? (
                          <Badge variant="info" className="ms-2">
                            <Trans>Carried debt</Trans>
                          </Badge>
                        ) : null}
                      </TableCell>
                      <TableCell>
                        {c.description ?? labels.chargeType[c.charge_type]}
                        {c.period ? (
                          <span className="text-xs text-muted-foreground"> · {c.period}</span>
                        ) : null}
                      </TableCell>
                      <TableCell>
                        <SiteDate value={c.due_date} timeZone={ctx.timeZone} />
                      </TableCell>
                      <TableCell className="text-end">
                        <Money minor={c.amount.amount} currency={ctx.currency} />
                      </TableCell>
                      <TableCell className="text-end">
                        <Money minor={c.paid.amount} currency={ctx.currency} />
                      </TableCell>
                      <TableCell className="text-end">
                        <Money minor={c.balance.amount} currency={ctx.currency} />
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={c.status} />
                      </TableCell>
                      <TableCell className="text-end whitespace-nowrap">
                        <Gate permission="money.post" fallback={null}>
                          {c.status !== 'cancelled' ? (
                            <>
                              <Button variant="ghost" size="sm" onClick={() => setCancelFor(c)}>
                                <Trans>Cancel charge</Trans>
                              </Button>
                            </>
                          ) : null}
                        </Gate>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
                <TableFooter>
                  <TableRow>
                    <TableCell colSpan={6}>
                      {data.next_cursor ? (
                        <Trans>Totals of the lines shown (without cancelled)</Trans>
                      ) : (
                        <Trans>Totals (without cancelled)</Trans>
                      )}
                    </TableCell>
                    <TableCell className="text-end">
                      <Money minor={sum((c) => c.amount.amount)} currency={ctx.currency} />
                    </TableCell>
                    <TableCell className="text-end">
                      <Money minor={sum((c) => c.paid.amount)} currency={ctx.currency} />
                    </TableCell>
                    <TableCell className="text-end">
                      <Money minor={sum((c) => c.balance.amount)} currency={ctx.currency} />
                    </TableCell>
                    <TableCell colSpan={2} />
                  </TableRow>
                </TableFooter>
              </Table>
              {data.next_cursor ? (
                <p className="text-sm text-muted-foreground">
                  <Trans>Showing the first 200 charges. Narrow the filters to see the rest.</Trans>
                </p>
              ) : null}
            </>
          )
        }}
      </QueryBoundary>
      {cancelFor ? (
        <CancelChargeDialog charge={cancelFor} ctx={ctx} onClose={() => setCancelFor(null)} />
      ) : null}
      {remindOpen ? (
        <RemindDialog ctx={ctx} selection={[...selected]} onClose={() => setRemindOpen(false)} />
      ) : null}
    </div>
  )
}

function useReversalNote(charge: S['Charge'], ctx: MoneyCtx) {
  return (
    <p className="rounded-md bg-muted p-3 text-sm">
      <Trans>
        The charge of <Money minor={charge.amount.amount} currency={ctx.currency} /> stays in the
        books. A reversal entry with your reason is created, so nothing is lost.
      </Trans>
    </p>
  )
}

function CancelChargeDialog({
  charge,
  ctx,
  onClose,
}: {
  charge: S['Charge']
  ctx: MoneyCtx
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async (reason: string) => {
    await moneyApi.POST('/v1/orgs/{org_id}/charges/{charge_id}:cancel', {
      params: { path: { org_id: ctx.orgId, charge_id: charge.id }, header: idemHeader(idem.key) },
      body: { reason },
    })
  })
  return (
    <ReasonDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t`Cancel this charge?`}
      description={`${charge.unit_door_number} · ${charge.description ?? ''}`}
      confirmLabel={t`Cancel charge`}
      destructive
      pending={m.isPending}
      error={error}
      onConfirm={(reason) =>
        m.mutateAsync(reason).then(
          () => {
            toast.info(t`Charge cancelled; a reversal was created.`)
            onClose()
          },
          (e) =>
            setError(
              e instanceof AppError && e.code === 'locked_period'
                ? t`This period is locked.`
                : t`Could not cancel the charge.`,
            ),
        )
      }
    >
      {useReversalNote(charge, ctx)}
    </ReasonDialog>
  )
}

function RemindDialog({
  ctx,
  selection,
  onClose,
}: {
  ctx: MoneyCtx
  selection: string[]
  onClose: () => void
}) {
  const { t } = useLingui()
  const labels = useLabels()
  const idem = useIdempotencyKey()
  const stages = useMoneyQuery(['dunning-stages', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/dunning-stages', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ).then((r) => r.items.filter((s) => s.enabled)),
  )
  const [stageId, setStageId] = useState('')
  const [channels, setChannels] = useState<Set<S['DunningStage']['channels'][number]>>(
    new Set(['in_app']),
  )
  const [error, setError] = useState<string>()
  const stage = stageId || stages.data?.[0]?.id || ''
  const body = useMemo<S['DunningRunRequest'] | null>(
    () =>
      stage && channels.size > 0
        ? {
            site_id: ctx.siteId,
            stage_id: stage,
            debtors: selection.length
              ? selection.map((k) => {
                  const [unit_id, debtor_kind] = k.split(':') as [string, 'owner' | 'tenant']
                  return unitSideRef(unit_id, debtor_kind)
                })
              : undefined,
            channels: [...channels],
          }
        : null,
    [ctx.siteId, stage, channels, selection],
  )
  const preview = useMoneyQuery(
    ['dunning-preview', body],
    () =>
      unwrap(
        moneyApi.POST('/v1/orgs/{org_id}/dunning-runs:preview', {
          params: { path: { org_id: ctx.orgId } },
          body: body!,
        }),
      ),
    { enabled: body !== null },
  )
  const run = useMoneyMutation(async () => {
    const res = await moneyApi.POST('/v1/orgs/{org_id}/dunning-runs', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: body!,
    })
    return res.data!
  })
  const toggleChannel = (c: S['DunningStage']['channels'][number]) =>
    setChannels((s) => {
      const n = new Set(s)
      if (n.has(c)) n.delete(c)
      else n.add(c)
      return n
    })
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>Send reminders</Trans>
          </DialogTitle>
          <DialogDescription>
            {selection.length > 0 ? (
              <Trans>To the {selection.length} selected unit sides with an overdue balance.</Trans>
            ) : (
              <Trans>
                To every unit side with an overdue balance. Select rows first to narrow it down.
              </Trans>
            )}
          </DialogDescription>
        </DialogHeader>
        <Field label={t`Stage`} htmlFor="rm-stage">
          <Select id="rm-stage" value={stage} onChange={(e) => setStageId(e.target.value)}>
            {(stages.data ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        </Field>
        <fieldset className="flex flex-wrap gap-4">
          <legend className="mb-1 text-sm font-medium">
            <Trans>Channels</Trans>
          </legend>
          {(['in_app', 'email', 'sms'] as const).map((c) => (
            <LabeledCheckbox
              key={c}
              label={labels.channel[c]}
              checked={channels.has(c)}
              onChange={() => toggleChannel(c)}
            />
          ))}
        </fieldset>
        {preview.data ? (
          <p role="status" className="text-sm">
            <Trans>
              {preview.data.recipient_count} recipients, {preview.data.sms_count} SMS,{' '}
              {preview.data.skipped_frozen} skipped (in legal collection).
            </Trans>
          </p>
        ) : null}
        <FormRootError message={error} />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            <Trans>Cancel</Trans>
          </Button>
          <Button
            disabled={!body || run.isPending || (preview.data?.recipient_count ?? 0) === 0}
            onClick={() =>
              run.mutateAsync().then(
                (r) => {
                  idem.renew()
                  toast.info(t`Reminders sent: ${r.event_count}.`)
                  onClose()
                },
                () => setError(t`Could not send the reminders.`),
              )
            }
          >
            <Trans>Send</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
