import { useMemo, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { useReauthGuard } from '@/components/ReauthDialog'
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
import { currencyDigits, minorToDecimalString } from '@/lib/money'
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
import { useAccounts, useUnits } from './pickers'
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
  const units = useUnits(ctx)
  const banks = useAccounts(ctx, 'bank')
  const boxes = useAccounts(ctx, 'cash_box')
  const idem = useIdempotencyKey()
  const guard = useReauthGuard()
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

  const csv = useMemo(() => {
    const lines = ['kind,unit,side,principal,late_fee,account_id']
    for (const u of units.data ?? []) {
      for (const side of ['owner', 'tenant'] as const) {
        const p = cells[`${u.id}:${side}:p`]
        const f = cells[`${u.id}:${side}:f`]
        if (p || f) lines.push(`unit_receivable,${u.door_number},${side},${dec(p)},${dec(f)},`)
      }
    }
    for (const a of banks.data ?? [])
      if (cells[`acc:${a.id}`]) lines.push(`bank,,,${dec(cells[`acc:${a.id}`])},0,${a.id}`)
    for (const a of boxes.data ?? [])
      if (cells[`acc:${a.id}`]) lines.push(`cash,,,${dec(cells[`acc:${a.id}`])},0,${a.id}`)
    return lines.join('\n')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cells, units.data, banks.data, boxes.data, digits])

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
  const dryRun = useMoneyMutation(async () => {
    const res = await send(true, crypto.randomUUID())
    return res.data!
  })
  const commit = useMoneyMutation(async () => {
    const done = await guard.run(() => send(false, idem.key))
    if (!done) throw new Error('reauth_cancelled')
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
                () => setError(t`The dry run failed.`),
              )
            }
          >
            <Trans>Check (dry run)</Trans>
          </Button>
          <Button
            disabled={locked || !report || report.error_rows > 0 || commit.isPending}
            onClick={() =>
              commit.mutateAsync().then(
                () => {
                  idem.renew()
                  setReport(null)
                  setCells({})
                  toast.info(t`Opening balances saved.`)
                },
                (e) =>
                  setError(
                    e instanceof Error && e.message === 'reauth_cancelled'
                      ? undefined
                      : t`Could not save the opening balances.`,
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
              <p className="text-sm text-muted-foreground">
                <Trans>
                  Total principal in this file:{' '}
                  <Money
                    minor={report.rows.reduce(
                      (a, r) =>
                        a +
                        Math.round(
                          Number((r.data as Record<string, string>).principal ?? 0) * 10 ** digits,
                        ),
                      0,
                    )}
                    currency={ctx.currency}
                  />
                </Trans>
              </p>
            ) : null}
          </div>
        ) : null}
        {guard.dialog}
      </CardContent>
    </Card>
  )
}

function EnteredCard({ ctx, items }: { ctx: MoneyCtx; items: S['OpeningBalance'][] }) {
  const { t } = useLingui()
  const units = useUnits(ctx)
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
                <TableCell>{o.kind}</TableCell>
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
    const done = await guard.run(() =>
      moneyApi.POST('/v1/orgs/{org_id}/opening-balances/{opening_balance_id}:void', {
        params: {
          path: { org_id: ctx.orgId, opening_balance_id: entry.id },
          header: idemHeader(idem.key),
        },
        body: { reason },
      }),
    )
    if (!done) throw new Error('reauth_cancelled')
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
            (e) =>
              setError(
                e instanceof Error && e.message === 'reauth_cancelled'
                  ? undefined
                  : t`Could not void the entry.`,
              ),
          )
        }
      />
      {guard.dialog}
    </>
  )
}
