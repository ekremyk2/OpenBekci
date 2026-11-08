import { useEffect, useMemo, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { useLocale } from '@/app/locale'
import { isReauthCancelled, useReauthGuard } from '@/components/ReauthDialog'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { MoneyInput } from '@/components/forms/MoneyInput'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toCsv } from '@/lib/csv'
import { currencyDigits, minorToDecimalString, parseMoneyInput } from '@/lib/money'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import {
  idemHeader,
  moneyApi,
  todayIn,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
} from './api'
import { useAccounts, useAllUnits, useBlocks } from './pickers'
import { useLabels } from './labels'
import { Field, FormRootError, Gate, PageHeader, QueryBoundary, ReasonDialog } from './ui'

type Cells = Record<string, bigint | null>

/**
 * Opening balances (M7): what each unit side owes at go-live, plus bank and cash balances. The
 * grid becomes a CSV that goes through the import endpoint, first as a dry run (nothing is saved),
 * then for real. The column layout is part of this screen's contract with the importer.
 */
export function OpeningBalancesPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const existing = useMoneyQuery(['openings', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/opening-balances', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 200 } },
      }),
    ).then((r) => r.items),
  )
  const lock = useMoneyQuery(['lock-date', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/sites/{site_id}/lock-date', {
        params: { path: { org_id: ctx.orgId, site_id: ctx.siteId } },
      }),
    ),
  )
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t`Opening balances`}
        description={t`Enter what every unit owes on the go-live date and what the bank and cash hold. A dry run shows problems before anything is saved.`}
      />
      <QueryBoundary query={existing} skeleton="page">
        {(items) => (
          <>
            {items.length > 0 ? <EnteredCard ctx={ctx} items={items} /> : null}
            <Gate permission="money.post">
              <Wizard ctx={ctx} lockDate={lock.data?.lock_date ?? null} />
            </Gate>
          </>
        )}
      </QueryBoundary>
    </div>
  )
}

function Wizard({ ctx, lockDate }: { ctx: MoneyCtx; lockDate: string | null }) {
  const { t } = useLingui()
  const units = useAllUnits(ctx)
  const blocks = useBlocks(ctx)
  const banks = useAccounts(ctx, 'bank')
  const boxes = useAccounts(ctx, 'cash_box')
  const idem = useIdempotencyKey()
  const dryIdem = useIdempotencyKey()
  const guard = useReauthGuard()
  const { intl } = useLocale()
  const [jobId, setJobId] = useState<string | null>(null)
  const [asOf, setAsOf] = useState(todayIn(ctx.timeZone))
  const [cells, setCells] = useState<Cells>({})
  const [file, setFile] = useState<File | null>(null)
  const [report, setReport] = useState<S['ImportReport'] | null>(null)
  const [error, setError] = useState<string>()
  const locked = lockDate !== null && asOf <= lockDate
  const digits = currencyDigits(ctx.currency)
  const dec = (v: bigint | null | undefined) => (v ? minorToDecimalString(v, digits) : '0')
  const set = (k: string, v: bigint | null) => {
    setReport(null)
    setCells((c) => ({ ...c, [k]: v }))
  }

  // File layout (shown on the screen): one row per unit side or account.
  //   kind, block, door_number, unit_id, side, principal, late_fee, account_id
  // A unit is identified by `unit_id`; `block` + `door_number` say the same thing in words (door
  // numbers repeat across blocks). Amounts are plain decimals with a dot ("1234.50"); cells with
  // commas or quotes are quoted (RFC 4180).
  const csv = useMemo(() => {
    const rows: (string | null)[][] = [OPENING_COLUMNS.slice()]
    const blockName = (id: string) => blocks.data?.find((b) => b.id === id)?.name ?? ''
    for (const u of units.data ?? []) {
      for (const side of ['owner', 'tenant'] as const) {
        const p = cells[`${u.id}:${side}:p`]
        const f = cells[`${u.id}:${side}:f`]
        if (p || f)
          rows.push([
            'unit_receivable',
            blockName(u.block_id),
            u.door_number,
            u.id,
            side,
            dec(p),
            dec(f),
            null,
          ])
      }
    }
    for (const a of banks.data ?? [])
      if (cells[`acc:${a.id}`])
        rows.push(['bank', null, null, null, null, dec(cells[`acc:${a.id}`]), '0', a.id])
    for (const a of boxes.data ?? [])
      if (cells[`acc:${a.id}`])
        rows.push(['cash', null, null, null, null, dec(cells[`acc:${a.id}`]), '0', a.id])
    return toCsv(rows)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cells, units.data, blocks.data, banks.data, boxes.data, digits])

  const source = () => file ?? new File([csv], 'opening-balances.csv', { type: 'text/csv' })
  const send = (dry: boolean, key: string) => {
    const f = source()
    return moneyApi.POST('/v1/orgs/{org_id}/opening-balances:import', {
      params: { path: { org_id: ctx.orgId }, query: { dry_run: dry }, header: idemHeader(key) },
      body: { site_id: ctx.siteId, as_of: asOf, file: f as unknown as string },
      bodySerializer: () => {
        const fd = new FormData()
        fd.set('site_id', ctx.siteId)
        fd.set('as_of', asOf)
        fd.set('file', f)
        return fd
      },
    })
  }
  // A key names one payload: once the grid or file changes, the next send gets a fresh key (the
  // server answers idempotency_mismatch when a key comes back with a different body).
  const payloadSig = `${asOf}|${file ? `${file.name}:${file.size}:${file.lastModified}` : csv}`
  useEffect(() => {
    idem.renew()
    dryIdem.renew()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [payloadSig])
  // The whole operation is re-authentication sensitive, the dry run included (M7).
  const dryRun = useMoneyMutation(async () => {
    const res = await guard.run(() => send(true, dryIdem.key))
    return res.data!
  })
  const commit = useMoneyMutation(async () => {
    const done = await guard.run(() => send(false, idem.key))
    return done.data!
  })
  const rowsCount = csv.split('\n').length - 1

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Enter opening balances</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <div className="max-w-xs">
          <Field
            label={t`Balances as of`}
            htmlFor="ob-asof"
            error={
              locked ? t`This date is on or before the lock date; the period is closed.` : undefined
            }
          >
            <Input
              id="ob-asof"
              type="date"
              dir="ltr"
              value={asOf}
              onChange={(e) => {
                setAsOf(e.target.value)
                setReport(null)
              }}
            />
          </Field>
          {lockDate ? (
            <p className="mt-1 text-xs text-muted-foreground">
              <Trans>
                Lock date: <SiteDate value={lockDate} timeZone={ctx.timeZone} />
              </Trans>
            </p>
          ) : null}
        </div>
        <QueryBoundary query={units as never} skeleton="table">
          {() => (
            <Table aria-label={t`Debts per unit side`}>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <Trans>Unit</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Owner: principal</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Owner: known late fee</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Tenant: principal</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Tenant: known late fee</Trans>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(units.data ?? []).map((u) => (
                  <TableRow key={u.id}>
                    <TableCell>{u.door_number}</TableCell>
                    {(['owner', 'tenant'] as const).flatMap((side) =>
                      (['p', 'f'] as const).map((kind) => (
                        <TableCell key={`${side}${kind}`} className="min-w-32">
                          <MoneyInput
                            aria-label={`${u.door_number} ${side === 'owner' ? t`Owner` : t`Tenant`} ${kind === 'p' ? t`principal` : t`late fee`}`}
                            currency={ctx.currency}
                            value={cells[`${u.id}:${side}:${kind}`] ?? null}
                            onChange={(v) => set(`${u.id}:${side}:${kind}`, v)}
                          />
                        </TableCell>
                      )),
                    )}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </QueryBoundary>
        <div className="grid gap-4 sm:grid-cols-2">
          {[...(banks.data ?? []), ...(boxes.data ?? [])].map((a) => (
            <Field key={a.id} label={`${a.name}`} htmlFor={`acc-${a.id}`}>
              <MoneyInput
                id={`acc-${a.id}`}
                currency={ctx.currency}
                value={cells[`acc:${a.id}`] ?? null}
                onChange={(v) => set(`acc:${a.id}`, v)}
              />
            </Field>
          ))}
        </div>
        <Field label={t`Or upload a spreadsheet instead (CSV or XLSX)`} htmlFor="ob-file">
          <Input
            id="ob-file"
            type="file"
            accept=".csv,.xlsx"
            onChange={(e) => {
              setFile(e.target.files?.[0] ?? null)
              setReport(null)
            }}
          />
        </Field>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            disabled={locked || dryRun.isPending || (!file && rowsCount === 0)}
            onClick={() =>
              dryRun.mutateAsync().then(
                (r) => {
                  setReport(r)
                  setError(undefined)
                },
                (e) => setError(isReauthCancelled(e) ? undefined : t`The dry run failed.`),
              )
            }
          >
            <Trans>Check (dry run)</Trans>
          </Button>
          <Button
            disabled={locked || !report || report.error_rows > 0 || commit.isPending}
            onClick={() =>
              commit.mutateAsync().then(
                (r) => {
                  idem.renew()
                  setReport(null)
                  setCells({})
                  setError(undefined)
                  // 202: the work runs in a job; the result is shown once it finishes
                  setJobId(r.job_id)
                },
                (e) =>
                  setError(
                    isReauthCancelled(e) ? undefined : t`Could not save the opening balances.`,
                  ),
              )
            }
          >
            <Trans>Save opening balances</Trans>
          </Button>
        </div>
        <FormRootError message={error} />
        {report ? (
          <div role="status" className="flex flex-col gap-2" aria-label={t`Dry run result`}>
            <p className="text-sm">
              <Trans>
                Dry run: {report.ok_rows} of {report.total_rows} rows are fine, {report.error_rows}{' '}
                have errors. Nothing was saved.
              </Trans>
            </p>
            {report.rows
              .filter((r) => r.status !== 'ok')
              .map((r) => (
                <p key={r.row} className="text-sm text-destructive">
                  <Trans>Row {r.row}</Trans>: {r.errors.map((e) => e.message ?? e.code).join('; ')}
                </p>
              ))}
            {report.error_rows === 0 ? (
              <DryRunTotals ctx={ctx} report={report} locale={intl} />
            ) : null}
          </div>
        ) : null}
        {jobId ? <ImportJob ctx={ctx} jobId={jobId} onClose={() => setJobId(null)} /> : null}
        <details className="text-sm" data-testid="file-layout">
          <summary className="cursor-pointer font-medium">
            <Trans>File layout for a spreadsheet upload</Trans>
          </summary>
          <div className="mt-2 flex flex-col gap-2 text-muted-foreground">
            <p>
              <Trans>One row per unit side or account, with these columns in the first row:</Trans>{' '}
              <code dir="ltr">{OPENING_COLUMNS.join(', ')}</code>
            </p>
            <ul className="list-disc ps-5">
              <li>
                <Trans>
                  kind: <code>unit_receivable</code>, <code>bank</code> or <code>cash</code>.
                </Trans>
              </li>
              <li>
                <Trans>
                  A unit is named by unit_id, or by block together with door_number: door numbers
                  repeat across blocks.
                </Trans>
              </li>
              <li>
                <Trans>side: owner or tenant. Accounts use account_id instead of a unit.</Trans>
              </li>
              <li>
                <Trans>
                  principal and late_fee are plain decimals such as 1234.50. A cell with a comma
                  goes in double quotes.
                </Trans>
              </li>
            </ul>
          </div>
        </details>
        {guard.dialog}
      </CardContent>
    </Card>
  )
}

const OPENING_COLUMNS = [
  'kind',
  'block',
  'door_number',
  'unit_id',
  'side',
  'principal',
  'late_fee',
  'account_id',
]

/**
 * Totals of a clean dry run in integer minor units: receivables apart from bank and cash balances.
 * Amounts are read with the locale's separators ("1.234,50" is 123450); an unreadable cell is
 * reported, never turned into NaN or guessed.
 */
export function dryRunTotals(
  rows: { data: unknown }[],
  currency: string,
  locale: string,
): { receivables: bigint; accounts: bigint; unreadable: number } {
  let receivables = 0n
  let accounts = 0n
  let unreadable = 0
  for (const r of rows) {
    const d = (r.data ?? {}) as Record<string, string>
    const minor = parseMoneyInput(String(d.principal ?? ''), currency, locale)
    if (minor === null) {
      unreadable += 1
      continue
    }
    if ((d.kind ?? 'unit_receivable') === 'unit_receivable') receivables += minor
    else accounts += minor
  }
  return { receivables, accounts, unreadable }
}

function DryRunTotals({
  ctx,
  report,
  locale,
}: {
  ctx: MoneyCtx
  report: S['ImportReport']
  locale: string
}) {
  const totals = dryRunTotals(report.rows, ctx.currency, locale)
  if (totals.unreadable > 0)
    return (
      <p className="text-sm text-destructive" role="alert">
        <Trans>{totals.unreadable} amounts could not be read, so no total is shown.</Trans>
      </p>
    )
  return (
    <p className="text-sm text-muted-foreground" data-testid="dry-run-totals">
      <Trans>
        Receivables in this file: <Money minor={totals.receivables} currency={ctx.currency} />. Bank
        and cash balances: <Money minor={totals.accounts} currency={ctx.currency} />.
      </Trans>
    </p>
  )
}

/** Follows the 202 job of a committed import until it finishes, then shows the result. */
function ImportJob({ ctx, jobId, onClose }: { ctx: MoneyCtx; jobId: string; onClose: () => void }) {
  const { t } = useLingui()
  const job = useMoneyQuery(
    ['job', jobId],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/jobs/{job_id}', {
          params: { path: { org_id: ctx.orgId, job_id: jobId } },
        }),
      ),
    {
      refetchInterval: (q) => {
        const s = q.state.data?.status
        return s === 'succeeded' || s === 'failed' || s === 'cancelled' ? false : 1000
      },
    },
  )
  const status = job.data?.status
  const done = status === 'succeeded'
  const failed = status === 'failed' || status === 'cancelled' || job.isError
  useEffect(() => {
    if (done) toast.info(t`Opening balances saved.`)
  }, [done, t])
  return (
    <div role="status" aria-label={t`Import progress`} className="rounded-md border p-3 text-sm">
      {done ? (
        <p>
          <Trans>The opening balances were imported.</Trans>
        </p>
      ) : failed ? (
        <p className="text-destructive">
          <Trans>
            The import did not finish. Nothing was posted; check the file and try again.
          </Trans>
        </p>
      ) : (
        <p>
          <Trans>Importing…</Trans>
          {job.data?.progress != null ? ` ${Math.round(job.data.progress * 100)}%` : ''}
        </p>
      )}
      {done || failed ? (
        <Button size="sm" variant="ghost" onClick={onClose}>
          <Trans>Dismiss</Trans>
        </Button>
      ) : null}
    </div>
  )
}

function EnteredCard({ ctx, items }: { ctx: MoneyCtx; items: S['OpeningBalance'][] }) {
  const { t } = useLingui()
  const units = useAllUnits(ctx)
  const labels = useLabels()
  const door = (id: string | null) => units.data?.find((u) => u.id === id)?.door_number ?? '–'
  const [voidFor, setVoidFor] = useState<S['OpeningBalance'] | null>(null)
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Already entered</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <Table aria-label={t`Entered opening balances`}>
          <TableHeader>
            <TableRow>
              <TableHead>
                <Trans>As of</Trans>
              </TableHead>
              <TableHead>
                <Trans>Kind</Trans>
              </TableHead>
              <TableHead>
                <Trans>Unit</Trans>
              </TableHead>
              <TableHead className="text-end">
                <Trans>Principal</Trans>
              </TableHead>
              <TableHead className="text-end">
                <Trans>Late fee</Trans>
              </TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((o) => (
              <TableRow
                key={o.id}
                className={o.status === 'voided' ? 'text-muted-foreground' : undefined}
              >
                <TableCell>
                  <SiteDate value={o.as_of} timeZone={ctx.timeZone} />
                </TableCell>
                <TableCell>{labels.openingKind[o.kind] ?? o.kind}</TableCell>
                <TableCell>{door(o.unit_id)}</TableCell>
                <TableCell className="text-end">
                  <Money minor={o.principal.amount} currency={ctx.currency} />
                </TableCell>
                <TableCell className="text-end">
                  <Money minor={o.late_fee.amount} currency={ctx.currency} />
                </TableCell>
                <TableCell className="text-end whitespace-nowrap">
                  {o.status === 'voided' ? (
                    <Badge variant="secondary">
                      <Trans>Voided</Trans>
                    </Badge>
                  ) : o.locked ? (
                    <Badge variant="secondary">
                      <Trans>Locked</Trans>
                    </Badge>
                  ) : (
                    <Gate permission="money.post" fallback={null}>
                      <Button size="sm" variant="ghost" onClick={() => setVoidFor(o)}>
                        <Trans>Void</Trans>
                      </Button>
                    </Gate>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {voidFor ? (
          <VoidOpening ctx={ctx} entry={voidFor} onClose={() => setVoidFor(null)} />
        ) : null}
      </CardContent>
    </Card>
  )
}

/** Voiding an opening entry needs a reason and a fresh sign-in; the replay keeps the same key. */
function VoidOpening({
  ctx,
  entry,
  onClose,
}: {
  ctx: MoneyCtx
  entry: S['OpeningBalance']
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const guard = useReauthGuard()
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async (reason: string) => {
    await guard.run(() =>
      moneyApi.POST('/v1/orgs/{org_id}/opening-balances/{opening_balance_id}:void', {
        params: {
          path: { org_id: ctx.orgId, opening_balance_id: entry.id },
          header: idemHeader(idem.key),
        },
        body: { reason },
      }),
    )
  })
  return (
    <>
      <ReasonDialog
        open
        onOpenChange={(o) => !o && onClose()}
        title={t`Void this opening entry?`}
        description={t`The entry stays visible as voided. You will be asked to confirm your password.`}
        confirmLabel={t`Void entry`}
        destructive
        pending={m.isPending}
        error={error}
        onConfirm={(reason) =>
          m.mutateAsync(reason).then(
            () => {
              idem.renew()
              onClose()
            },
            (e) => setError(isReauthCancelled(e) ? undefined : t`Could not void the entry.`),
          )
        }
      />
      {guard.dialog}
    </>
  )
}
