import { useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { SidePicker, type DebtorSide } from '@/components/forms/SidePicker'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useIdempotencyKey } from '@/lib/idempotency'
import { cn } from '@/lib/utils'
import { toast } from '@/lib/toast'
import {
  idemHeader,
  moneyApi,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
} from './api'
import { AccountPicker, UnitPicker } from './pickers'
import { Field, FormRootError, Gate, PageHeader, QueryBoundary, Select, StatusBadge } from './ui'
import { Input } from '@/components/ui/input'

/** One Idempotency-Key per row action, kept until it succeeds so a retry reuses it. */
export function useIdempotencyKeys() {
  const keys = useRef(new Map<string, string>())
  return {
    get(id: string) {
      let k = keys.current.get(id)
      if (!k) {
        k = crypto.randomUUID()
        keys.current.set(id, k)
      }
      return k
    },
    done(id: string) {
      keys.current.delete(id)
    },
  }
}

/* ───────────────────────── imports list + upload ───────────────────────── */

export function BankImportsPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const q = useMoneyQuery(['bank-imports', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/bank-statement-imports', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 50 } },
      }),
    ).then((r) => r.items),
  )
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t`Bank statement import`}
        description={t`Upload the bank's CSV or Excel export, then confirm which unit each line belongs to. Nothing is matched without you.`}
      />
      <Gate permission="money.post" fallback={null}>
        <UploadCard ctx={ctx} />
      </Gate>
      <QueryBoundary
        query={q}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No imports yet`}
        emptyDescription={t`Upload a statement above to start matching.`}
      >
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Period</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Importer</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>Lines</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>To match</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>Closing balance</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Status</Trans>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((i) => (
                <TableRow key={i.id}>
                  <TableCell>
                    <Link
                      to="/orgs/$orgId/money/bank/$importId"
                      params={{ orgId: ctx.orgId, importId: i.id }}
                      className="underline-offset-4 hover:underline"
                    >
                      {i.period_from && i.period_to ? (
                        <>
                          <SiteDate value={i.period_from} timeZone={ctx.timeZone} /> –{' '}
                          <SiteDate value={i.period_to} timeZone={ctx.timeZone} />
                        </>
                      ) : (
                        i.file_id
                      )}
                    </Link>
                  </TableCell>
                  <TableCell>{i.importer}</TableCell>
                  <TableCell className="text-end tabular-nums">{i.transaction_count}</TableCell>
                  <TableCell className="text-end tabular-nums">{i.unmatched_count}</TableCell>
                  <TableCell className="text-end">
                    {i.closing_balance ? (
                      <Money minor={i.closing_balance.amount} currency={ctx.currency} />
                    ) : (
                      '–'
                    )}
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={i.status} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>
    </div>
  )
}

function UploadCard({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const navigate = useNavigate()
  const idem = useIdempotencyKey()
  const importers = useMoneyQuery(['bank-importers'], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/bank-importers', {
        params: { path: { org_id: ctx.orgId }, query: { limit: 50 } },
      }),
    ).then((r) => r.items),
  )
  const [file, setFile] = useState<File | null>(null)
  const [importer, setImporter] = useState('')
  const [account, setAccount] = useState('')
  const [error, setError] = useState<string>()
  const ext = file?.name.split('.').pop()?.toLowerCase() ?? ''
  const usable = (importers.data ?? []).filter((i) => !ext || i.formats.includes(ext))
  const chosen = usable.some((i) => i.id === importer) ? importer : (usable[0]?.id ?? '')
  const m = useMoneyMutation(async () => {
    const res = await moneyApi.POST('/v1/orgs/{org_id}/bank-statement-imports', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: {
        site_id: ctx.siteId,
        file: file as unknown as string,
        importer: chosen,
        asset_account_id: account,
      },
      bodySerializer: () => {
        const fd = new FormData()
        fd.set('site_id', ctx.siteId)
        fd.set('file', file!)
        fd.set('importer', chosen)
        fd.set('asset_account_id', account)
        return fd
      },
    })
    return res.data!
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Upload a statement</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <form
          className="grid gap-4 sm:grid-cols-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (!file || !account || !chosen)
              return setError(t`Choose the file, the importer and the bank account.`)
            setError(undefined)
            m.mutateAsync().then(
              async (imp) => {
                idem.renew()
                await navigate({
                  to: '/orgs/$orgId/money/bank/$importId',
                  params: { orgId: ctx.orgId, importId: imp.id },
                })
              },
              () => setError(t`The file could not be imported.`),
            )
          }}
        >
          <Field label={t`File (CSV or XLSX)`} htmlFor="imp-file">
            <Input
              id="imp-file"
              type="file"
              accept=".csv,.xlsx"
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            />
          </Field>
          <Field label={t`Importer`} htmlFor="imp-importer">
            <Select id="imp-importer" value={chosen} onChange={(e) => setImporter(e.target.value)}>
              {usable.map((i) => (
                <option key={i.id} value={i.id}>
                  {i.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t`Bank account`} htmlFor="imp-account">
            <AccountPicker
              ctx={ctx}
              subtype="bank"
              id="imp-account"
              value={account}
              onChange={setAccount}
            />
          </Field>
          <div className="flex items-end">
            <Button type="submit" disabled={m.isPending}>
              <Trans>Upload</Trans>
            </Button>
          </div>
          <div className="sm:col-span-4">
            <FormRootError message={error} />
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

/* ───────────────────────── matching ───────────────────────── */

export function BankMatchPage({ importId }: { importId: string }) {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const imp = useMoneyQuery(['bank-import', importId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/bank-statement-imports/{import_id}', {
        params: { path: { org_id: ctx.orgId, import_id: importId } },
      }),
    ),
  )
  const recon = useMoneyQuery(['bank-import-recon', importId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/bank-statement-imports/{import_id}/reconciliation', {
        params: { path: { org_id: ctx.orgId, import_id: importId } },
      }),
    ),
  )
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t`Match bank lines`}
        description={t`J and K move between lines, Enter confirms the suggested unit, I ignores a line. Lines without a suggestion need a manual match.`}
        actions={
          <Link
            to="/orgs/$orgId/money/bank"
            params={{ orgId: ctx.orgId }}
            className="text-sm underline-offset-4 hover:underline"
          >
            <Trans>All imports</Trans>
          </Link>
        }
      />
      <QueryBoundary query={imp} skeleton="page">
        {(i) => (
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                {i.file_id} <StatusBadge status={i.status} />
              </CardTitle>
            </CardHeader>
            <CardContent>
              <QueryBoundary query={recon} skeleton="page">
                {(r) => (
                  <dl
                    className="grid gap-4 text-sm sm:grid-cols-5"
                    aria-label={t`Reconciliation balances`}
                  >
                    <Bal label={t`Statement opening`} v={r.statement_opening} ctx={ctx} />
                    <Bal label={t`Statement closing`} v={r.statement_closing} ctx={ctx} />
                    <Bal label={t`Ledger opening`} v={r.ledger_opening} ctx={ctx} />
                    <Bal label={t`Ledger closing`} v={r.ledger_closing} ctx={ctx} />
                    <div>
                      <dt className="text-muted-foreground">
                        <Trans>Difference</Trans>
                      </dt>
                      <dd
                        className={cn(
                          'text-lg font-semibold',
                          r.difference.amount !== 0 && 'text-destructive',
                        )}
                        data-testid="recon-diff"
                      >
                        <Money minor={r.difference.amount} currency={ctx.currency} />
                      </dd>
                    </div>
                  </dl>
                )}
              </QueryBoundary>
            </CardContent>
          </Card>
        )}
      </QueryBoundary>
      <MatchList ctx={ctx} importId={importId} />
    </div>
  )
}

function Bal({ label, v, ctx }: { label: string; v: S['Money'] | null; ctx: MoneyCtx }) {
  return (
    <div>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-lg font-semibold">
        {v ? <Money minor={v.amount} currency={ctx.currency} /> : '–'}
      </dd>
    </div>
  )
}

export function MatchList({ ctx, importId }: { ctx: MoneyCtx; importId: string }) {
  const { t } = useLingui()
  const [showAll, setShowAll] = useState(false)
  const [cursor, setCursor] = useState(0)
  const [manualFor, setManualFor] = useState<string | null>(null)
  const keys = useIdempotencyKeys()
  const q = useMoneyQuery(['bank-tx', importId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/bank-transactions', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, import_id: importId, limit: 500 },
        },
      }),
    ).then((r) => r.items),
  )
  const rows = useMemo(
    () =>
      (q.data ?? []).filter(
        (x) => showAll || x.match_status === 'suggested' || x.match_status === 'unmatched',
      ),
    [q.data, showAll],
  )
  const current = rows[Math.min(cursor, rows.length - 1)]
  const match = useMoneyMutation(
    async (a: { tx: S['BankTransaction']; unit_id: string; side: DebtorSide }) => {
      await moneyApi.POST('/v1/orgs/{org_id}/bank-transactions/{transaction_id}:match', {
        params: {
          path: { org_id: ctx.orgId, transaction_id: a.tx.id },
          header: idemHeader(keys.get(a.tx.id)),
        },
        body: { unit_id: a.unit_id, debtor_kind: a.side },
      })
      keys.done(a.tx.id)
    },
  )
  const ignore = useMoneyMutation(async (tx: S['BankTransaction']) => {
    await moneyApi.POST('/v1/orgs/{org_id}/bank-transactions/{transaction_id}:ignore', {
      params: {
        path: { org_id: ctx.orgId, transaction_id: tx.id },
        header: idemHeader(keys.get(`i-${tx.id}`)),
      },
      body: {},
    })
    keys.done(`i-${tx.id}`)
  })
  const unmatch = useMoneyMutation(async (tx: S['BankTransaction']) => {
    await moneyApi.POST('/v1/orgs/{org_id}/bank-transactions/{transaction_id}:unmatch', {
      params: {
        path: { org_id: ctx.orgId, transaction_id: tx.id },
        header: idemHeader(keys.get(`u-${tx.id}`)),
      },
      body: {},
    })
    keys.done(`u-${tx.id}`)
  })

  function confirm(tx: S['BankTransaction']) {
    const s = tx.suggestions[0]
    if (!s) return setManualFor(tx.id)
    match.mutate(
      { tx, unit_id: s.unit_id, side: s.debtor_kind },
      {
        onSuccess: () => toast.info(t`Matched to ${s.unit_door_number}.`),
        onError: () => toast.error(t`Could not match the line.`),
      },
    )
  }

  function onKeyDown(e: React.KeyboardEvent) {
    const target = e.target as HTMLElement
    if (['INPUT', 'SELECT', 'TEXTAREA'].includes(target.tagName)) return
    if (e.key === 'j') {
      e.preventDefault()
      setCursor((c) => Math.min(c + 1, rows.length - 1))
    } else if (e.key === 'k') {
      e.preventDefault()
      setCursor((c) => Math.max(c - 1, 0))
    } else if (
      e.key === 'Enter' &&
      current &&
      (current.match_status === 'suggested' || current.match_status === 'unmatched')
    ) {
      e.preventDefault()
      confirm(current)
    } else if (e.key === 'i' && current && current.match_status !== 'matched') {
      e.preventDefault()
      ignore.mutate(current)
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          className="size-4 accent-primary"
          checked={showAll}
          onChange={(e) => setShowAll(e.target.checked)}
        />
        <Trans>Show matched and ignored lines too</Trans>
      </label>
      <QueryBoundary
        query={q}
        isEmpty={() => rows.length === 0}
        emptyTitle={t`Everything is matched`}
        emptyDescription={t`No lines are waiting for you.`}
      >
        {() => (
          <div
            tabIndex={0}
            role="grid"
            aria-label={t`Bank lines`}
            aria-activedescendant={current ? `tx-${current.id}` : undefined}
            onKeyDown={onKeyDown}
            className="rounded-md outline-none focus-visible:ring-[3px] focus-visible:ring-ring"
          >
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <Trans>Date</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Counterparty</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Description</Trans>
                  </TableHead>
                  <TableHead className="text-end">
                    <Trans>Amount</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Suggestion</Trans>
                  </TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((tx, i) => {
                  const selected = current?.id === tx.id
                  const s = tx.suggestions[0]
                  return (
                    <TableRow
                      key={tx.id}
                      id={`tx-${tx.id}`}
                      role="row"
                      aria-selected={selected}
                      data-status={tx.match_status}
                      onClick={() => setCursor(i)}
                      className={cn(
                        selected && 'bg-accent',
                        tx.match_status === 'ignored' && 'text-muted-foreground',
                      )}
                    >
                      <TableCell>
                        <SiteDate value={tx.booked_at} timeZone={ctx.timeZone} />
                      </TableCell>
                      <TableCell>{tx.counterparty ?? '–'}</TableCell>
                      <TableCell>{tx.description ?? '–'}</TableCell>
                      <TableCell className="text-end">
                        <Money minor={tx.amount.amount} currency={ctx.currency} />
                      </TableCell>
                      <TableCell>
                        {tx.match_status === 'matched' ? (
                          <StatusBadge status="matched" />
                        ) : tx.match_status === 'ignored' ? (
                          <StatusBadge status="ignored" />
                        ) : s ? (
                          <span>
                            <Badge variant="info">{s.unit_door_number}</Badge>{' '}
                            <Badge variant="outline">
                              {s.debtor_kind === 'tenant' ? t`Tenant` : t`Owner`}
                            </Badge>
                            <span className="ms-2 text-xs text-muted-foreground">{s.reason}</span>
                          </span>
                        ) : (
                          <span className="text-sm text-muted-foreground">
                            <Trans>No suggestion</Trans>
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="text-end whitespace-nowrap">
                        <Gate permission="payments.record" fallback={null}>
                          {tx.match_status === 'matched' ? (
                            <Button size="sm" variant="ghost" onClick={() => unmatch.mutate(tx)}>
                              <Trans>Unmatch</Trans>
                            </Button>
                          ) : tx.match_status === 'ignored' ? null : (
                            <>
                              {s ? (
                                <Button size="sm" onClick={() => confirm(tx)}>
                                  <Trans>Confirm</Trans>
                                </Button>
                              ) : null}
                              <Button
                                size="sm"
                                variant="outline"
                                onClick={() => setManualFor(tx.id)}
                              >
                                <Trans>Match manually</Trans>
                              </Button>
                              <Button size="sm" variant="ghost" onClick={() => ignore.mutate(tx)}>
                                <Trans>Ignore</Trans>
                              </Button>
                            </>
                          )}
                        </Gate>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </QueryBoundary>
      {manualFor ? (
        <ManualMatch
          ctx={ctx}
          onCancel={() => setManualFor(null)}
          onMatch={(unit_id, side) => {
            const tx = q.data?.find((x) => x.id === manualFor)
            if (!tx) return
            match.mutate(
              { tx, unit_id, side },
              {
                onSuccess: () => setManualFor(null),
                onError: () => toast.error(t`Could not match the line.`),
              },
            )
          }}
        />
      ) : null}
    </div>
  )
}

function ManualMatch({
  ctx,
  onMatch,
  onCancel,
}: {
  ctx: MoneyCtx
  onMatch: (unitId: string, side: DebtorSide) => void
  onCancel: () => void
}) {
  const { t } = useLingui()
  const [unit, setUnit] = useState('')
  const [side, setSide] = useState<DebtorSide>('owner')
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Match manually</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-wrap items-end gap-4">
        <Field label={t`Unit`} htmlFor="mm-unit">
          <UnitPicker ctx={ctx} id="mm-unit" value={unit} onChange={setUnit} />
        </Field>
        <Field label={t`Side`}>
          <SidePicker value={side} onChange={setSide} />
        </Field>
        <Button disabled={!unit} onClick={() => onMatch(unit, side)}>
          <Trans>Match</Trans>
        </Button>
        <Button variant="ghost" onClick={onCancel}>
          <Trans>Cancel</Trans>
        </Button>
      </CardContent>
    </Card>
  )
}
