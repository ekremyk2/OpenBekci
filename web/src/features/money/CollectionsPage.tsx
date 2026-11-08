import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { downloadFile } from '@/api/download'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { MoneyInput } from '@/components/forms/MoneyInput'
import { SidePicker, type DebtorSide } from '@/components/forms/SidePicker'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
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
import {
  idemHeader,
  ifMatch,
  minorMoney,
  moneyApi,
  todayIn,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
} from './api'
import { useLabels } from './labels'
import { DebtorPicker, debtorRef, emptyTarget, targetId, type DebtorTarget } from './DebtorPicker'
import { UnitPicker, usePersons, useAllUnits } from './pickers'
import {
  LabeledCheckbox,
  ExportButtons,
  Field,
  FormRootError,
  Gate,
  PageHeader,
  QueryBoundary,
  SideBadge,
  StatusBadge,
  Textarea,
} from './ui'

type Channel = S['DunningStage']['channels'][number]

export function CollectionsPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t`Collections`}
        description={t`Reminder stages, the history of what was sent to a debtor, and legal cases.`}
      />
      <StagesCard ctx={ctx} />
      <TimelineCard ctx={ctx} />
      <CasesCard ctx={ctx} />
    </div>
  )
}

function StagesCard({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const labels = useLabels()
  const idem = useIdempotencyKey()
  const q = useMoneyQuery(['dunning-stages', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/dunning-stages', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ).then((r) => r.items),
  )
  const patch = useMoneyMutation(
    async (a: { id: string; version: number; body: S['DunningStageUpdate'] }) => {
      await moneyApi.PATCH('/v1/orgs/{org_id}/dunning-stages/{stage_id}', {
        params: { path: { org_id: ctx.orgId, stage_id: a.id }, header: ifMatch(a.version) },
        body: a.body,
      })
    },
  )
  const remove = useMoneyMutation(async (s: S['DunningStage']) => {
    await moneyApi.DELETE('/v1/orgs/{org_id}/dunning-stages/{stage_id}', {
      params: { path: { org_id: ctx.orgId, stage_id: s.id }, header: ifMatch(s.version) },
    })
  })
  const add = useMoneyMutation(
    async (b: { name: string; days: number; kind: 'reminder' | 'ihtarname' }) => {
      await moneyApi.POST('/v1/orgs/{org_id}/dunning-stages', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: {
          site_id: ctx.siteId,
          name: b.name,
          kind: b.kind,
          days_after_due: b.days,
          channels: ['in_app'],
        },
      })
    },
  )
  const [name, setName] = useState('')
  const [days, setDays] = useState('14')
  const [kind, setKind] = useState<'reminder' | 'ihtarname'>('reminder')
  const [error, setError] = useState<string>()
  const toggleChannel = (s: S['DunningStage'], c: Channel) =>
    patch.mutate({
      id: s.id,
      version: s.version,
      body: {
        channels: s.channels.includes(c) ? s.channels.filter((x) => x !== c) : [...s.channels, c],
      },
    })
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Dunning stages</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <QueryBoundary query={q} isEmpty={(d) => d.length === 0} emptyTitle={t`No stages`}>
          {(items) => (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <Trans>Stage</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Kind</Trans>
                  </TableHead>
                  <TableHead className="text-end">
                    <Trans>Days after due</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Channels</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Enabled</Trans>
                  </TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell>{s.name}</TableCell>
                    <TableCell>{s.kind === 'ihtarname' ? t`Ihtarname` : t`Reminder`}</TableCell>
                    <TableCell className="text-end tabular-nums">{s.days_after_due}</TableCell>
                    <TableCell>
                      <div className="flex gap-3">
                        {(['in_app', 'email', 'sms'] as const).map((c) => (
                          <Gate
                            key={c}
                            permission="collections.manage"
                            fallback={
                              s.channels.includes(c) ? (
                                <Badge variant="outline">{labels.channel[c]}</Badge>
                              ) : null
                            }
                          >
                            <LabeledCheckbox
                              label={labels.channel[c]}
                              checked={s.channels.includes(c)}
                              onChange={() => toggleChannel(s, c)}
                            />
                          </Gate>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Gate
                        permission="collections.manage"
                        fallback={<StatusBadge status={s.enabled ? 'active' : 'paused'} />}
                      >
                        <LabeledCheckbox
                          label={s.name}
                          className="[&>span]:sr-only"
                          aria-label={`${t`Enabled`}: ${s.name}`}
                          checked={s.enabled}
                          onChange={(e) =>
                            patch.mutate({
                              id: s.id,
                              version: s.version,
                              body: { enabled: e.target.checked },
                            })
                          }
                        />
                      </Gate>
                    </TableCell>
                    <TableCell className="text-end">
                      <Gate permission="collections.manage" fallback={null}>
                        <Button size="sm" variant="ghost" onClick={() => remove.mutate(s)}>
                          <Trans>Delete</Trans>
                        </Button>
                      </Gate>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </QueryBoundary>
        <Gate permission="collections.manage" fallback={null}>
          <form
            className="flex flex-wrap items-end gap-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (!name.trim() || !(Number(days) >= 0))
                return setError(t`Enter a name and the days after the due date.`)
              setError(undefined)
              add.mutateAsync({ name: name.trim(), days: Number(days), kind }).then(
                () => {
                  idem.renew()
                  setName('')
                },
                () => setError(t`Could not add the stage.`),
              )
            }}
          >
            <Field label={t`New stage`} htmlFor="ds-name">
              <Input id="ds-name" value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field label={t`Days after due`} htmlFor="ds-days">
              <Input
                id="ds-days"
                type="number"
                min={0}
                value={days}
                onChange={(e) => setDays(e.target.value)}
              />
            </Field>
            <Field label={t`Kind`} htmlFor="ds-kind">
              <select
                id="ds-kind"
                className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                value={kind}
                onChange={(e) => setKind(e.target.value as 'reminder' | 'ihtarname')}
              >
                <option value="reminder">{t`Reminder`}</option>
                <option value="ihtarname">{t`Ihtarname`}</option>
              </select>
            </Field>
            <Button type="submit" variant="outline">
              <Trans>Add stage</Trans>
            </Button>
          </form>
          <FormRootError message={error} />
        </Gate>
      </CardContent>
    </Card>
  )
}

function TimelineCard({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const labels = useLabels()
  const [unit, setUnit] = useState('')
  const [side, setSide] = useState<DebtorSide>('owner')
  const stages = useMoneyQuery(['dunning-stages', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/dunning-stages', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ).then((r) => r.items),
  )
  const events = useMoneyQuery(
    ['dunning-events', unit, side],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/dunning-events', {
          params: {
            path: { org_id: ctx.orgId },
            query: { site_id: ctx.siteId, unit_id: unit, debtor_kind: side },
          },
        }),
      ).then((r) => r.items),
    { enabled: !!unit },
  )
  const stageName = (id: string) => stages.data?.find((s) => s.id === id)?.name ?? id
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Reminder history of a unit side</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-wrap items-end gap-4">
          <Field label={t`Unit`} htmlFor="tl-unit">
            <UnitPicker ctx={ctx} id="tl-unit" value={unit} onChange={setUnit} />
          </Field>
          <Field label={t`Side`}>
            <SidePicker value={side} onChange={setSide} />
          </Field>
          {unit ? (
            <ExportButtons
              endpoint="/v1/orgs/{org_id}/debt-breakdown"
              query={{ site_id: ctx.siteId, unit_id: unit, debtor_kind: side }}
              basename="debt-breakdown"
              formats={['pdf', 'xlsx']}
            />
          ) : null}
        </div>
        {!unit ? (
          <p className="text-sm text-muted-foreground">
            <Trans>Choose a unit to see what was sent to it.</Trans>
          </p>
        ) : (
          <QueryBoundary
            query={events}
            isEmpty={(d) => d.length === 0}
            emptyTitle={t`Nothing sent yet`}
          >
            {(items) => (
              <ol className="flex flex-col gap-2" aria-label={t`Timeline`}>
                {items.map((e) => (
                  <li
                    key={e.id}
                    className="flex flex-wrap items-center gap-3 rounded-md border p-3 text-sm"
                  >
                    <SiteDate value={e.sent_at} timeZone={ctx.timeZone} kind="datetime" />
                    <span className="font-medium">{stageName(e.stage_id)}</span>
                    <Badge variant="outline">{labels.channel[e.channel] ?? e.channel}</Badge>
                    {e.document_file_id ? (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() =>
                          downloadFile(
                            {
                              endpoint: '/v1/orgs/{org_id}/files/{file_id}',
                              path: { org_id: ctx.orgId, file_id: e.document_file_id! },
                            },
                            `ihtarname-${e.id}.pdf`,
                          ).catch(() => toast.error(t`The download failed. Please try again.`))
                        }
                      >
                        <Trans>Download ihtarname</Trans>
                      </Button>
                    ) : null}
                  </li>
                ))}
              </ol>
            )}
          </QueryBoundary>
        )}
      </CardContent>
    </Card>
  )
}

function CasesCard({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const units = useAllUnits(ctx)
  const persons = usePersons(ctx)
  const debtorName = (c: S['CollectionCase']) =>
    c.unit_id
      ? (units.data?.find((u) => u.id === c.unit_id)?.door_number ?? c.unit_id)
      : (persons.data?.find((p) => p.id === c.debtor_person_id)?.display_name ??
        c.debtor_person_id ??
        '–')
  const [creating, setCreating] = useState(false)
  const [closing, setClosing] = useState<S['CollectionCase'] | null>(null)
  const q = useMoneyQuery(['cases', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/collection-cases', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items),
  )
  const freezes = useMoneyQuery(['late-fee-freezes', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/late-fee-freezes', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, active: true } },
      }),
    ).then((r) => r.items),
  )
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle>
          <Trans>Collection cases</Trans>
        </CardTitle>
        <Gate permission="collections.manage" fallback={null}>
          <Button size="sm" onClick={() => setCreating(true)}>
            <Trans>Open a case</Trans>
          </Button>
        </Gate>
      </CardHeader>
      <CardContent>
        <QueryBoundary
          query={q}
          isEmpty={(d) => d.length === 0}
          emptyTitle={t`No cases`}
          emptyDescription={t`Open a case when a debt goes to a lawyer; fee accrual is frozen for that unit side.`}
        >
          {(items) => (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <Trans>Unit</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Side</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Opened</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Lawyer</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>File no.</Trans>
                  </TableHead>
                  <TableHead className="text-end">
                    <Trans>Costs charged</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Status</Trans>
                  </TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((c) => {
                  const frozen = freezes.data?.some((f) => f.case_id === c.id)
                  return (
                    <TableRow key={c.id}>
                      <TableCell>{debtorName(c)}</TableCell>
                      <TableCell>
                        <SideBadge side={c.debtor_kind} />
                      </TableCell>
                      <TableCell>
                        <SiteDate value={c.opened_on} timeZone={ctx.timeZone} />
                      </TableCell>
                      <TableCell>{c.lawyer ?? '–'}</TableCell>
                      <TableCell>{c.file_no ?? '–'}</TableCell>
                      <TableCell className="text-end">
                        <Money minor={c.costs_charged.amount} currency={ctx.currency} />
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={c.status} />
                        {frozen ? (
                          <Badge variant="info" className="ms-2">
                            <Trans>Fees frozen</Trans>
                          </Badge>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-end">
                        {c.status === 'open' ? (
                          <Gate permission="collections.manage" fallback={null}>
                            <Button size="sm" variant="ghost" onClick={() => setClosing(c)}>
                              <Trans>Close case</Trans>
                            </Button>
                          </Gate>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </QueryBoundary>
      </CardContent>
      {creating ? <NewCase ctx={ctx} onClose={() => setCreating(false)} /> : null}
      {closing ? <CloseCase ctx={ctx} c={closing} onClose={() => setClosing(null)} /> : null}
    </Card>
  )
}

function NewCase({ ctx, onClose }: { ctx: MoneyCtx; onClose: () => void }) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [target, setTarget] = useState<DebtorTarget>(emptyTarget())
  const [lawyer, setLawyer] = useState('')
  const [fileNo, setFileNo] = useState('')
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async () => {
    await moneyApi.POST('/v1/orgs/{org_id}/collection-cases', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: {
        site_id: ctx.siteId,
        debtor: debtorRef(target),
        opened_on: todayIn(ctx.timeZone),
        lawyer: lawyer || null,
        file_no: fileNo || null,
      },
    })
  })
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>Open a collection case</Trans>
          </DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            if (!targetId(target)) return setError(t`Choose who owes.`)
            m.mutateAsync().then(
              () => {
                idem.renew()
                toast.info(
                  target.mode === 'unit'
                    ? t`Case opened; late fees are frozen for this unit side.`
                    : t`Case opened.`,
                )
                onClose()
              },
              () => setError(t`Could not open the case.`),
            )
          }}
        >
          <DebtorPicker ctx={ctx} idPrefix="nc" value={target} onChange={setTarget} />
          <Field label={t`Lawyer`} htmlFor="nc-lawyer">
            <Input id="nc-lawyer" value={lawyer} onChange={(e) => setLawyer(e.target.value)} />
          </Field>
          <Field label={t`File number`} htmlFor="nc-file">
            <Input id="nc-file" value={fileNo} onChange={(e) => setFileNo(e.target.value)} />
          </Field>
          <FormRootError message={error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={m.isPending}>
              <Trans>Open case</Trans>
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function CloseCase({
  ctx,
  c,
  onClose,
}: {
  ctx: MoneyCtx
  c: S['CollectionCase']
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [costs, setCosts] = useState<bigint | null>(null)
  const [note, setNote] = useState('')
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async () => {
    await moneyApi.POST('/v1/orgs/{org_id}/collection-cases/{case_id}:close', {
      params: { path: { org_id: ctx.orgId, case_id: c.id }, header: idemHeader(idem.key) },
      body: {
        costs: costs && costs > 0n ? minorMoney(costs, ctx.currency) : undefined,
        note: note || undefined,
      },
    })
  })
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>Close case</Trans>
          </DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            m.mutateAsync().then(
              () => {
                idem.renew()
                onClose()
              },
              () => setError(t`Could not close the case.`),
            )
          }}
        >
          <Field
            label={t`Legal costs to charge back to the debtor (optional)`}
            htmlFor="cc-costs"
            hint={t`Creates a legal-cost charge on the debtor's side.`}
          >
            <MoneyInput id="cc-costs" currency={ctx.currency} value={costs} onChange={setCosts} />
          </Field>
          <Field label={t`Note`} htmlFor="cc-note">
            <Textarea id="cc-note" value={note} onChange={(e) => setNote(e.target.value)} />
          </Field>
          <FormRootError message={error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={m.isPending}>
              <Trans>Close case</Trans>
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
