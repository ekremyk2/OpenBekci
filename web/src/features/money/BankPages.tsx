import { useMemo, useState } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
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
  moneyKey,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
  unitSideRef,
} from './api'
import { useIdempotencyKeys } from './forms'
import { DebtorPicker, debtorRef, emptyTarget, type DebtorTarget } from './DebtorPicker'
import { AccountPicker, useAllUnits } from './pickers'
import { Field, FormRootError, Gate, PageHeader, QueryBoundary, Select, StatusBadge } from './ui'
import { Input } from '@/components/ui/input'

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
                        i.filename
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
                {i.filename} <StatusBadge status={i.status} />
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
  // A statement of a few hundred lines is normal: page by cursor, never above the contract cap (200).
  const q = useInfiniteQuery({
    queryKey: moneyKey(ctx.orgId, 'bank-tx', importId),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/bank-transactions', {
          params: {
            path: { org_id: ctx.orgId },
            query: { site_id: ctx.siteId, import_id: importId, limit: 200, cursor: pageParam },
          },
        }),
      ),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    staleTime: 0,
  })
  const lines = useMemo(() => q.data?.pages.flatMap((p) => p.items) ?? [], [q.data])
  // Declared transfers awaiting confirmation belong on this screen: matching a line to one confirms
  // that payment with the line, so the money is booked once (contract: `payment_id` on the match).
  const declared = useMoneyQuery(['declared-payments', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/payments', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, status: 'pending_declared', limit: 200 },
        },
      }),
    ).then((r) => r.items),
  )
  const candidatesFor = (tx: S['BankTransaction']) => declaredCandidates(declared.data ?? [], tx)
  const rows = useMemo(
    () =>
      lines.filter(
        (x) => showAll || x.match_status === 'suggested' || x.match_status === 'unmatched',
      ),
    [lines, showAll],
  )
  const current = rows[Math.min(cursor, rows.length - 1)]
  const match = useMoneyMutation(
    async (a: {
      tx: S['BankTransaction']
      debtor?: ReturnType<typeof debtorRef>
      paymentId?: string
    }) => {
      await moneyApi.POST('/v1/orgs/{org_id}/bank-transactions/{transaction_id}:match', {
        params: {
          path: { org_id: ctx.orgId, transaction_id: a.tx.id },
          header: idemHeader(keys.get(a.tx.id)),
        },
        // a declared payment replaces the debtor: the payment already says who paid
        body: a.paymentId ? { payment_id: a.paymentId } : { debtor: a.debtor },
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
    const dp = candidatesFor(tx).find((c) => c.exact)
    if (dp)
      return match.mutate(
        { tx, paymentId: dp.payment.id },
        {
          onSuccess: () => toast.info(t`Linked to the declared payment.`),
          onError: () => toast.error(t`Could not match the line.`),
        },
      )
    if (!s) return setManualFor(tx.id)
    match.mutate(
      { tx, debtor: unitSideRef(s.unit_id, s.debtor_kind) },
      {
        onSuccess: () => toast.info(t`Matched to ${s.unit_door_number}.`),
        onError: () => toast.error(t`Could not match the line.`),
      },
    )
  }

  function onKeyDown(e: React.KeyboardEvent) {
    // Only the grid itself reacts. A focused button, link, field or anything inside a dialog keeps
    // its own keys: Enter on "Ignore" must never confirm a different row.
    if (!isGridKey(e)) return
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
        query={{
          isPending: q.isPending,
          isError: q.isError,
          error: q.error,
          refetch: q.refetch as never,
          data: lines,
        }}
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
                        <SiteDate value={tx.booked_on} timeZone={ctx.timeZone} />
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
                        ) : candidatesFor(tx).some((c) => c.exact) ? (
                          <span>
                            <Badge variant="warning">
                              <Trans>Declared transfer</Trans>
                            </Badge>{' '}
                            <span className="text-xs text-muted-foreground">
                              {candidatesFor(tx).find((c) => c.exact)!.payment.payer_display_name ??
                                ''}
                            </span>
                          </span>
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
      {q.hasNextPage ? (
        <div className="flex justify-center">
          <Button
            variant="outline"
            onClick={() => void q.fetchNextPage()}
            disabled={q.isFetchingNextPage}
          >
            <Trans>Load more lines</Trans>
          </Button>
        </div>
      ) : null}
      {manualFor ? (
        <ManualMatch
          ctx={ctx}
          declared={candidatesFor(lines.find((x) => x.id === manualFor)!)}
          onCancel={() => setManualFor(null)}
          onMatch={(choice) => {
            const tx = lines.find((x) => x.id === manualFor)
            if (!tx) return
            match.mutate(
              { tx, ...choice },
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

/** True only when the key event comes from the grid element itself, not from a control inside it. */
export function isGridKey(e: React.KeyboardEvent): boolean {
  if (e.target !== e.currentTarget) return false
  const el = e.target as HTMLElement
  if (el.closest('[role="dialog"],[role="alertdialog"],dialog')) return false
  if (
    el.matches(
      'input,select,textarea,button,a[href],[contenteditable="true"],[tabindex]:not([role="grid"])',
    )
  )
    return false
  return !(e.metaKey || e.ctrlKey || e.altKey)
}

/**
 * Declared transfers that could belong to a bank line, best first: same amount and the unit side
 * the line is suggested for; then the same amount; then the rest. `exact` is safe to pre-select.
 */
export function declaredCandidates(declared: S['Payment'][], tx: S['BankTransaction']) {
  if (tx.amount.amount <= 0) return []
  const suggestion = tx.suggestions[0]
  return declared
    .map((payment) => {
      const sameAmount = payment.gross.amount === tx.amount.amount
      const sameUnit =
        !!suggestion &&
        payment.unit_id === suggestion.unit_id &&
        payment.debtor_kind === suggestion.debtor_kind
      return {
        payment,
        exact: sameAmount && (sameUnit || !suggestion),
        rank: (sameAmount ? 0 : 2) + (sameUnit ? 0 : 1),
      }
    })
    .sort((a, b) => a.rank - b.rank)
}

function ManualMatch({
  ctx,
  declared,
  onMatch,
  onCancel,
}: {
  ctx: MoneyCtx
  declared: ReturnType<typeof declaredCandidates>
  onMatch: (choice: { debtor?: ReturnType<typeof debtorRef>; paymentId?: string }) => void
  onCancel: () => void
}) {
  const { t } = useLingui()
  const [target, setTarget] = useState<DebtorTarget>(emptyTarget())
  const [paymentId, setPaymentId] = useState('')
  const ready = paymentId !== '' || (target.mode === 'unit' ? !!target.unitId : !!target.personId)
  const units = useAllUnits(ctx)
  const door = (id: string | null) => units.data?.find((u) => u.id === id)?.door_number ?? '–'
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Match manually</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {declared.length > 0 ? (
          <Field label={t`Link to declared payment`} htmlFor="mm-declared">
            <Select
              id="mm-declared"
              value={paymentId}
              onChange={(e) => setPaymentId(e.target.value)}
            >
              <option value="">{t`No, match to a unit instead`}</option>
              {declared.map(({ payment: p, exact }) => (
                <option key={p.id} value={p.id}>
                  {door(p.unit_id)} · {p.payer_display_name ?? '–'} · {p.gross.amount}
                  {exact ? ' ✓' : ''}
                </option>
              ))}
            </Select>
          </Field>
        ) : null}
        <div className="flex flex-wrap items-end gap-4">
          {paymentId === '' ? (
            <DebtorPicker ctx={ctx} idPrefix="mm" value={target} onChange={setTarget} />
          ) : null}
          <Button
            disabled={!ready}
            onClick={() => onMatch(paymentId ? { paymentId } : { debtor: debtorRef(target) })}
          >
            <Trans>Match</Trans>
          </Button>
          <Button variant="ghost" onClick={onCancel}>
            <Trans>Cancel</Trans>
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}
