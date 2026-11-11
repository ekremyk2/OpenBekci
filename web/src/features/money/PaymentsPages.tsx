import { useEffect, useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm, useWatch } from 'react-hook-form'
import { Trans, useLingui } from '@lingui/react/macro'
import { z } from 'zod'
import { downloadFile } from '@/api/download'
import { AppError } from '@/api/errors'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { MoneyInput } from '@/components/forms/MoneyInput'
import type { DebtorSide } from '@/components/forms/SidePicker'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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
import { toContractDate } from '@/lib/contract-dates'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import {
  idemHeader,
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
import { isoDate, MoneyFormField, positiveMinor, useZodMessages } from './forms'
import { DebtorPicker, debtorRef, emptyTarget, targetId, type DebtorTarget } from './DebtorPicker'
import { useLabels } from './labels'
import { useSession } from '@/app/session'
import {
  AccountPicker,
  PersonPicker,
  useAccounts,
  useCashBoxes,
  useMyCashBox,
  useAllUnits,
} from './pickers'
import { CardRefundDialog } from './CheckoutsPages'
import {
  applyServerErrors,
  LabeledCheckbox,
  Field,
  FormRootError,
  Gate,
  PageHeader,
  QueryBoundary,
  ReasonDialog,
  Select,
  SideBadge,
  StatusBadge,
  Textarea,
} from './ui'

/* ───────────────────────── receipts ───────────────────────── */

export function ReceiptActions({ ctx, payment }: { ctx: MoneyCtx; payment: S['Payment'] }) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [sms, setSms] = useState(true)
  const [email, setEmail] = useState(false)
  const send = useMoneyMutation(async () => {
    const channels = [...(sms ? (['sms'] as const) : []), ...(email ? (['email'] as const) : [])]
    await moneyApi.POST('/v1/orgs/{org_id}/payments/{payment_id}:send-receipt', {
      params: { path: { org_id: ctx.orgId, payment_id: payment.id }, header: idemHeader(idem.key) },
      body: { channels },
    })
  })
  if (!payment.receipt_no) return null
  return (
    <div className="flex flex-wrap items-center gap-3">
      <Button
        variant="outline"
        size="sm"
        onClick={() =>
          downloadFile(
            {
              endpoint: '/v1/orgs/{org_id}/payments/{payment_id}/receipt',
              path: { org_id: ctx.orgId, payment_id: payment.id },
              format: 'pdf',
            },
            `receipt-${payment.receipt_no!.replace('/', '-')}.pdf`,
          ).catch(() => toast.error(t`The download failed. Please try again.`))
        }
      >
        <Trans>Download receipt (PDF)</Trans>
      </Button>
      <LabeledCheckbox label={t`SMS`} checked={sms} onChange={(e) => setSms(e.target.checked)} />
      <LabeledCheckbox
        label={t`Email`}
        checked={email}
        onChange={(e) => setEmail(e.target.checked)}
      />
      <Button
        variant="outline"
        size="sm"
        disabled={send.isPending || (!sms && !email)}
        onClick={() =>
          send.mutateAsync().then(
            () => toast.info(t`Receipt sent.`),
            () => toast.error(t`Could not send the receipt.`),
          )
        }
      >
        <Trans>Send receipt</Trans>
      </Button>
    </div>
  )
}

/* ───────────────────────── list ───────────────────────── */

export function PaymentsPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const labels = useLabels()
  const [status, setStatus] = useState('')
  const [rejectFor, setRejectFor] = useState<S['Payment'] | null>(null)
  const [confirmFor, setConfirmFor] = useState<S['Payment'] | null>(null)
  const [voidFor, setVoidFor] = useState<S['Payment'] | null>(null)
  const [refundFor, setRefundFor] = useState<S['Payment'] | null>(null)
  const units = useAllUnits(ctx)
  const door = (id: string | null) => units.data?.find((u) => u.id === id)?.door_number ?? '–'
  const q = useMoneyQuery(['payments', ctx.siteId, status], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/payments', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, status: status || undefined, limit: 200 },
        },
      }),
    ),
  )
  // The declared-transfers card has its own query: an old declaration must not hide behind newer
  // confirmed rows in a truncated all-status page.
  const declaredQ = useMoneyQuery(['payments-declared', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/payments', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, status: 'pending_declared', limit: 200 },
        },
      }),
    ).then((r) => r.items),
  )
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t`Payments`}
        description={t`Every receipt gets the next number in the site's gap-free sequence. A mistake is voided with a reason, never deleted.`}
        actions={
          <>
            <Link
              to="/orgs/$orgId/money/payments/cash-boxes"
              params={{ orgId: ctx.orgId }}
              className={buttonVariants({ variant: 'outline' })}
            >
              <Trans>Cash boxes</Trans>
            </Link>
            <Gate permission="payments.record" fallback={null}>
              <Link
                to="/orgs/$orgId/money/payments/new"
                params={{ orgId: ctx.orgId }}
                className={buttonVariants()}
              >
                <Trans>Record payment</Trans>
              </Link>
            </Gate>
          </>
        }
      />
      <QueryBoundary
        query={q}
        isEmpty={(d) => d.items.length === 0 && status === '' && !declaredQ.data?.length}
        emptyTitle={t`No payments yet`}
        emptyDescription={t`Record the first cash receipt, or import a bank statement.`}
      >
        {({ items, next_cursor }) => {
          const declared = declaredQ.data ?? []
          return (
            <>
              {declared.length > 0 ? (
                <Card>
                  <CardHeader>
                    <CardTitle>
                      <Trans>Declared by residents, awaiting confirmation</Trans>
                    </CardTitle>
                  </CardHeader>
                  <CardContent>
                    <Table aria-label={t`Declared transfers`}>
                      <TableBody>
                        {declared.map((p) => (
                          <TableRow key={p.id}>
                            <TableCell>{door(p.unit_id)}</TableCell>
                            <TableCell>
                              <SideBadge side={p.debtor_kind} />
                            </TableCell>
                            <TableCell>{p.payer_display_name ?? '–'}</TableCell>
                            <TableCell>
                              <SiteDate value={p.received_on} timeZone={ctx.timeZone} />
                            </TableCell>
                            <TableCell className="text-end">
                              <Money minor={p.gross.amount} currency={ctx.currency} />
                            </TableCell>
                            <TableCell>{p.reference}</TableCell>
                            <TableCell className="text-end whitespace-nowrap">
                              <Gate permission="payments.record" fallback={null}>
                                <Button size="sm" onClick={() => setConfirmFor(p)}>
                                  <Trans>Confirm</Trans>
                                </Button>
                                <Button size="sm" variant="ghost" onClick={() => setRejectFor(p)}>
                                  <Trans>Reject</Trans>
                                </Button>
                              </Gate>
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </CardContent>
                </Card>
              ) : null}
              <div className="flex items-end gap-3">
                <Field label={t`Status`} htmlFor="pf-status">
                  <Select
                    id="pf-status"
                    className="w-auto"
                    value={status}
                    onChange={(e) => setStatus(e.target.value)}
                  >
                    <option value="">{t`Any`}</option>
                    <option value="confirmed">{t`Confirmed`}</option>
                    <option value="pending_declared">{t`Awaiting confirmation`}</option>
                    <option value="voided">{t`Voided`}</option>
                    <option value="refunded">{t`Refunded`}</option>
                  </Select>
                </Field>
              </div>
              {next_cursor ? (
                <p className="text-sm text-muted-foreground">
                  <Trans>
                    Showing the first 200 payments. Filter by status to narrow the list.
                  </Trans>
                </p>
              ) : null}
              {items.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  <Trans>No payments match.</Trans>
                </p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>
                        <Trans>Receipt</Trans>
                      </TableHead>
                      <TableHead>
                        <Trans>Date</Trans>
                      </TableHead>
                      <TableHead>
                        <Trans>Unit</Trans>
                      </TableHead>
                      <TableHead>
                        <Trans>Side</Trans>
                      </TableHead>
                      <TableHead>
                        <Trans>Payer</Trans>
                      </TableHead>
                      <TableHead>
                        <Trans>Method</Trans>
                      </TableHead>
                      <TableHead className="text-end">
                        <Trans>Amount</Trans>
                      </TableHead>
                      <TableHead>
                        <Trans>Status</Trans>
                      </TableHead>
                      <TableHead />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {items.map((p) => (
                      <TableRow key={p.id}>
                        <TableCell className="tabular-nums">{p.receipt_no ?? '–'}</TableCell>
                        <TableCell>
                          <SiteDate value={p.received_on} timeZone={ctx.timeZone} />
                        </TableCell>
                        <TableCell>{door(p.unit_id)}</TableCell>
                        <TableCell>
                          <SideBadge side={p.debtor_kind} />
                        </TableCell>
                        <TableCell>{p.payer_display_name ?? '–'}</TableCell>
                        <TableCell>
                          {labels.method[p.method] ??
                            (p.method.startsWith('card:')
                              ? `${labels.method.card} (${p.method.slice(5)})`
                              : p.method)}
                        </TableCell>
                        <TableCell className="text-end">
                          <Money minor={p.gross.amount} currency={ctx.currency} />
                        </TableCell>
                        <TableCell>
                          <StatusBadge status={p.status} />
                          {p.bank_transaction_id ? (
                            <Badge variant="outline" className="ms-1">
                              <Trans>Reconciled</Trans>
                            </Badge>
                          ) : null}
                        </TableCell>
                        <TableCell className="text-end whitespace-nowrap">
                          {p.status === 'confirmed' ? (
                            <>
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() =>
                                  downloadFile(
                                    {
                                      endpoint: '/v1/orgs/{org_id}/payments/{payment_id}/receipt',
                                      path: { org_id: ctx.orgId, payment_id: p.id },
                                      format: 'pdf',
                                    },
                                    `receipt-${(p.receipt_no ?? p.id).replace('/', '-')}.pdf`,
                                  ).catch(() =>
                                    toast.error(t`The download failed. Please try again.`),
                                  )
                                }
                              >
                                <Trans>Receipt</Trans>
                              </Button>
                              {p.method.startsWith('card:') ? (
                                <Gate permission="money.post" fallback={null}>
                                  <Button variant="ghost" size="sm" onClick={() => setRefundFor(p)}>
                                    <Trans>Refund</Trans>
                                  </Button>
                                </Gate>
                              ) : (
                                <Gate permission="payments.record" fallback={null}>
                                  <Button variant="ghost" size="sm" onClick={() => setVoidFor(p)}>
                                    <Trans>Void</Trans>
                                  </Button>
                                </Gate>
                              )}
                            </>
                          ) : null}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </>
          )
        }}
      </QueryBoundary>
      {confirmFor ? (
        <ConfirmDeclaredDialog ctx={ctx} payment={confirmFor} onClose={() => setConfirmFor(null)} />
      ) : null}
      {rejectFor ? (
        <RejectDialog ctx={ctx} payment={rejectFor} onClose={() => setRejectFor(null)} />
      ) : null}
      {voidFor ? <VoidDialog ctx={ctx} payment={voidFor} onClose={() => setVoidFor(null)} /> : null}
      {refundFor ? (
        <CardRefundDialog ctx={ctx} payment={refundFor} onClose={() => setRefundFor(null)} />
      ) : null}
    </div>
  )
}

function ConfirmDeclaredDialog({
  ctx,
  payment,
  bankTransactionId,
  onClose,
}: {
  ctx: MoneyCtx
  payment: S['Payment']
  bankTransactionId?: string
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const banks = useAccounts(ctx, 'bank')
  const [account, setAccount] = useState('')
  const [lineId, setLineId] = useState(bankTransactionId ?? '')
  // Unmatched incoming bank lines of the same amount: confirming with one books the money once and
  // marks the line reconciled (contract: `bank_transaction_id`), instead of matching it again later.
  const lines = useMoneyQuery(['declared-lines', ctx.siteId, payment.id], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/bank-transactions', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, limit: 200 },
        },
      }),
    ).then((r) =>
      r.items.filter(
        (x) =>
          x.amount.amount === payment.gross.amount &&
          (x.match_status === 'unmatched' || x.match_status === 'suggested'),
      ),
    ),
  )
  const acct = account || banks.data?.[0]?.id || payment.asset_account_id
  const m = useMoneyMutation(async () => {
    const res = await moneyApi.POST('/v1/orgs/{org_id}/payments/{payment_id}:confirm', {
      params: { path: { org_id: ctx.orgId, payment_id: payment.id }, header: idemHeader(idem.key) },
      body: { asset_account_id: acct, bank_transaction_id: lineId || undefined },
    })
    return res.data!
  })
  const [err, setErr] = useState<string>()
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>Confirm this transfer</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>
              The money arrived in the bank:{' '}
              <Money minor={payment.gross.amount} currency={ctx.currency} />. It is applied to the
              oldest debt first and gets a receipt number.
            </Trans>
          </DialogDescription>
        </DialogHeader>
        <Field label={t`Received in`} htmlFor="cd-acct">
          <AccountPicker ctx={ctx} subtype="bank" id="cd-acct" value={acct} onChange={setAccount} />
        </Field>
        {(lines.data ?? []).length > 0 ? (
          <Field
            label={t`Bank line it arrived as`}
            htmlFor="cd-line"
            hint={t`Linking the line marks it reconciled, so it cannot be matched a second time.`}
          >
            <Select id="cd-line" value={lineId} onChange={(e) => setLineId(e.target.value)}>
              <option value="">{t`Not linked`}</option>
              {(lines.data ?? []).map((l) => (
                <option key={l.id} value={l.id}>
                  {l.booked_on} · {l.counterparty ?? '–'} · {l.description ?? ''}
                </option>
              ))}
            </Select>
          </Field>
        ) : null}
        <FormRootError message={err} />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            <Trans>Cancel</Trans>
          </Button>
          <Button
            disabled={m.isPending}
            onClick={() =>
              m.mutateAsync().then(
                (p) => {
                  idem.renew()
                  toast.info(t`Confirmed. Receipt ${p.receipt_no ?? ''}`)
                  onClose()
                },
                () => setErr(t`Could not confirm the payment.`),
              )
            }
          >
            <Trans>Confirm payment</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function RejectDialog({
  ctx,
  payment,
  onClose,
}: {
  ctx: MoneyCtx
  payment: S['Payment']
  onClose: () => void
}) {
  const { t } = useLingui()
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async (reason: string) => {
    await moneyApi.POST('/v1/orgs/{org_id}/payments/{payment_id}:reject', {
      params: { path: { org_id: ctx.orgId, payment_id: payment.id } },
      body: { reason },
    })
  })
  return (
    <ReasonDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t`Reject this declared transfer?`}
      confirmLabel={t`Reject`}
      destructive
      pending={m.isPending}
      error={error}
      onConfirm={(reason) =>
        m.mutateAsync(reason).then(onClose, () => setError(t`Could not reject the payment.`))
      }
    />
  )
}

function VoidDialog({
  ctx,
  payment,
  onClose,
}: {
  ctx: MoneyCtx
  payment: S['Payment']
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async (reason: string) => {
    await moneyApi.POST('/v1/orgs/{org_id}/payments/{payment_id}:void', {
      params: { path: { org_id: ctx.orgId, payment_id: payment.id }, header: idemHeader(idem.key) },
      body: { reason },
    })
  })
  return (
    <ReasonDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t`Void receipt ${payment.receipt_no ?? ''}?`}
      description={t`The receipt number stays in the sequence, marked as voided. The charges it paid become open again.`}
      confirmLabel={t`Void payment`}
      destructive
      pending={m.isPending}
      error={error}
      onConfirm={(reason) =>
        m
          .mutateAsync(reason)
          .then(onClose, (e) =>
            setError(
              e instanceof Error && 'code' in e && (e as { code: string }).code === 'locked_period'
                ? t`This period is locked.`
                : t`Could not void the payment.`,
            ),
          )
      }
    />
  )
}

/* ───────────────────────── record a payment ───────────────────────── */

export function NewPaymentPage() {
  const ctx = useMoneyCtx()!
  return (
    <Gate permission="payments.record">
      <RecordPaymentForm ctx={ctx} />
    </Gate>
  )
}

/** Account subtype that can take each payment method: cash goes to a cash box, transfers to a bank. */
/** The server says a box is short with a field error on `amount` coded `insufficient_balance`. */
function transferShortfall(e: AppError): boolean {
  return (
    e.isValidation && (e.fieldErrors.amount ?? []).some((i) => i.code === 'insufficient_balance')
  )
}

export const METHOD_SUBTYPE = { cash: 'cash_box', bank_transfer: 'bank' } as const

type AllocRow = {
  charge_id: string
  description: string | null
  balance: number
  amount: bigint | null
}

function RecordPaymentForm({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const labels = useLabels()
  const msg = useZodMessages()
  const idem = useIdempotencyKey()
  const { can } = useSession()
  // A collector (payments.record without money.read) sees a minimal page: cash into their own box.
  const collector = !can('money.read')
  const mine = useMyCashBox(ctx)
  const banks = useAccounts(ctx, 'bank')
  const allAccounts = useAccounts(ctx)
  const [target, setTarget] = useState<DebtorTarget>(emptyTarget())
  const [manual, setManual] = useState(false)
  const [edits, setEdits] = useState<Record<string, bigint | null>>({})
  const [receipt, setReceipt] = useState<S['Payment'] | null>(null)
  const [sendSms, setSendSms] = useState(false)
  const schema = useMemo(
    () =>
      z.object({
        unit_id: z.string().min(1, msg.pick),
        payer_person_id: z.string(),
        method: z.enum(['cash', 'bank_transfer']),
        amount: positiveMinor(msg.amount),
        received_at: isoDate(msg.date),
        asset_account_id: z.string().min(1, msg.pick),
        reference: z.string(),
        note: z.string(),
      }),
    [msg],
  )
  type V = z.infer<typeof schema>
  const form = useForm<V>({
    resolver: zodResolver(schema),
    defaultValues: {
      unit_id: '',
      payer_person_id: '',
      method: 'cash',
      amount: null as unknown as bigint,
      received_at: todayIn(ctx.timeZone),
      asset_account_id: '',
      reference: '',
      note: '',
    },
  })
  const v = useWatch({ control: form.control })
  const method = v.method ?? 'cash'
  // Defaults follow the method: cash lands in the caller's own box (never someone else's), a
  // transfer in the site's bank account. A manager who collects no cash picks the box.
  const defaultAccount =
    method === 'cash' ? (mine.box?.account_id ?? '') : (banks.data?.[0]?.id ?? '')
  useEffect(() => {
    const current = form.getValues('asset_account_id')
    const subtype = allAccounts.data?.find((a) => a.id === current)?.subtype
    const fits = !current || subtype === undefined || subtype === METHOD_SUBTYPE[method]
    if (!current || !fits) form.setValue('asset_account_id', defaultAccount)
  }, [defaultAccount, method, allAccounts.data, form])
  const account = v.asset_account_id || defaultAccount
  const amount = typeof v.amount === 'bigint' && v.amount > 0n ? v.amount : null
  const open = useMoneyQuery(
    ['open-charges', JSON.stringify(debtorRef(target))],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/charges', {
          params: {
            path: { org_id: ctx.orgId },
            query:
              target.mode === 'unit'
                ? {
                    site_id: ctx.siteId,
                    unit_id: target.unitId,
                    debtor_kind: target.side,
                    limit: 100,
                  }
                : { site_id: ctx.siteId, person_id: target.personId, limit: 100 },
          },
        }),
      ).then((r) =>
        r.items
          .filter((c) => c.balance.amount > 0 && c.status !== 'cancelled')
          .sort((a, b) => a.due_date.localeCompare(b.due_date)),
      ),
    { enabled: !!v.unit_id },
  )
  // The server settles oldest charge first and keeps the rest as credit; the preview is the same rule here.
  const preview = useMemo(() => {
    if (!open.data || !amount) return null
    let left = amount
    const allocations = open.data.flatMap((c) => {
      const take = left < BigInt(c.balance.amount) ? left : BigInt(c.balance.amount)
      left -= take
      return take > 0n ? [{ charge_id: c.id, charge_description: c.description ?? c.period, amount: Number(take) }] : []
    })
    return { allocations, credit: Number(left) }
  }, [open.data, amount])
  const startManual = () => setManual(true)
  // Manual rows start from the automatic allocation; typed amounts override it per charge.
  const rows = useMemo<AllocRow[]>(() => {
    if (!manual || !open.data) return []
    const auto = new Map(
      (preview?.allocations ?? []).map((al) => [al.charge_id, al.amount]),
    )
    return open.data.map((c) => ({
      charge_id: c.id,
      description: c.description ?? c.period,
      balance: c.balance.amount,
      amount: c.id in edits ? edits[c.id]! : auto.has(c.id) ? BigInt(auto.get(c.id)!) : null,
    }))
  }, [manual, open.data, preview, edits])
  const allocated = rows.reduce((a, r) => a + (r.amount ?? 0n), 0n)
  const rowError =
    manual &&
    (rows.some((r) => r.amount !== null && (r.amount < 0n || r.amount > BigInt(r.balance))) ||
      (amount !== null && allocated > amount))

  const create = useMoneyMutation(async (values: V) => {
    const res = await moneyApi.POST('/v1/orgs/{org_id}/payments', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: {
        site_id: ctx.siteId,
        debtor: debtorRef(target),
        payer_person_id: values.payer_person_id || undefined,
        method: values.method,
        amount: minorMoney(values.amount, ctx.currency),
        ...toContractDate('received', values.received_at),
        asset_account_id: values.asset_account_id || account,
        reference: values.reference || undefined,
        note: values.note || undefined,
        allocations: manual
          ? rows
              .filter((r) => r.amount && r.amount > 0n)
              .map((r) => ({ charge_id: r.charge_id, amount: minorMoney(r.amount!, ctx.currency) }))
          : undefined,
        send_receipt: sendSms ? { channels: ['sms'] } : undefined,
      },
    })
    return res.data!
  })
  async function onSubmit(values: V) {
    if (rowError) return
    const chosen = allAccounts.data?.find((a) => a.id === (values.asset_account_id || account))
    if (chosen && chosen.subtype !== METHOD_SUBTYPE[values.method]) {
      form.setError('asset_account_id', {
        message:
          values.method === 'cash'
            ? t`Cash goes into a cash box, not a bank account.`
            : t`A transfer goes into a bank account, not a cash box.`,
      })
      return
    }
    try {
      const p = await create.mutateAsync(values)
      idem.renew()
      setReceipt(p)
    } catch (e) {
      applyServerErrors(form, e, t`Could not record the payment.`, {
        locked_period: t`This period is locked.`,
      })
    }
  }
  const err = form.formState.errors

  if (receipt) {
    return (
      <div className="flex max-w-xl flex-col gap-4">
        <PageHeader title={t`Payment recorded`} />
        <Card>
          <CardContent className="flex flex-col gap-3 pt-6">
            <p>
              <Trans>Receipt number</Trans>:{' '}
              <strong data-testid="receipt-no" className="tabular-nums">
                {receipt.receipt_no}
              </strong>
            </p>
            <p className="text-sm">
              <Trans>
                <Money minor={receipt.gross.amount} currency={ctx.currency} /> received,{' '}
                <Money minor={receipt.allocated.amount} currency={ctx.currency} /> applied to
                charges, <Money minor={receipt.unallocated.amount} currency={ctx.currency} /> kept
                as credit.
              </Trans>
            </p>
            <ReceiptActions ctx={ctx} payment={receipt} />
            <div className="flex gap-2">
              <Button
                onClick={() => {
                  setReceipt(null)
                  setManual(false)
                  setEdits({})
                  form.reset({
                    ...form.getValues(),
                    amount: null as unknown as bigint,
                    reference: '',
                    note: '',
                  })
                }}
              >
                <Trans>Record another</Trans>
              </Button>
              <Link
                to="/orgs/$orgId/money/statements"
                params={{ orgId: ctx.orgId }}
                search={{
                  unit: receipt.unit_id ?? undefined,
                  side: (receipt.debtor_kind as DebtorSide) ?? undefined,
                  person: receipt.unit_id ? undefined : (receipt.payer_person_id ?? undefined),
                }}
                className={buttonVariants({ variant: 'outline' })}
              >
                <Trans>See statement</Trans>
              </Link>
            </div>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={collector ? t`Record cash` : t`Record a payment`}
        description={
          collector
            ? t`Cash you collected. It goes into your own cash box and is applied to the oldest debt first.`
            : t`Cash or a transfer you received. It is applied to the oldest debt first, principal before fee, unless you choose otherwise.`
        }
      />
      <div className="grid gap-6 lg:grid-cols-2">
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
        >
          <DebtorPicker
            ctx={ctx}
            idPrefix="pay"
            value={target}
            error={err.unit_id?.message}
            onChange={(next) => {
              setTarget(next)
              form.setValue('unit_id', targetId(next), { shouldValidate: true })
              setManual(false)
              setEdits({})
            }}
          />
          <Field label={t`Paid by (optional)`} htmlFor="pay-person">
            <PersonPicker
              ctx={ctx}
              id="pay-person"
              value={v.payer_person_id ?? ''}
              onChange={(id) => form.setValue('payer_person_id', id)}
              placeholder={t`The side's person`}
            />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <MoneyFormField
              id="pay-amount"
              control={form.control}
              name="amount"
              label={t`Amount`}
              currency={ctx.currency}
              error={err.amount?.message}
            />
            <Field label={t`Received on`} htmlFor="pay-date" error={err.received_at?.message}>
              <Input id="pay-date" type="date" dir="ltr" {...form.register('received_at')} />
            </Field>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            {collector ? null : (
              <Field label={t`Method`} htmlFor="pay-method">
                <Select id="pay-method" {...form.register('method')}>
                  <option value="cash">{labels.method.cash}</option>
                  <option value="bank_transfer">{labels.method.bank_transfer}</option>
                </Select>
              </Field>
            )}
            <Field
              label={t`Received into`}
              htmlFor="pay-account"
              error={err.asset_account_id?.message}
            >
              <AccountPicker
                ctx={ctx}
                subtype={METHOD_SUBTYPE[method]}
                only={collector && mine.box ? [mine.box.account_id] : undefined}
                id="pay-account"
                value={account}
                onChange={(id) => form.setValue('asset_account_id', id, { shouldValidate: true })}
              />
            </Field>
          </div>
          <Field label={t`Reference (optional)`} htmlFor="pay-ref">
            <Input id="pay-ref" {...form.register('reference')} />
          </Field>
          <Field label={t`Note (optional)`} htmlFor="pay-note">
            <Textarea id="pay-note" {...form.register('note')} />
          </Field>
          <LabeledCheckbox
            label={t`Send the receipt by SMS`}
            checked={sendSms}
            onChange={(e) => setSendSms(e.target.checked)}
          />
          <FormRootError message={err.root?.message} />
          <div>
            <Button type="submit" disabled={form.formState.isSubmitting || !!rowError}>
              <Trans>Record payment</Trans>
            </Button>
          </div>
        </form>
        <Card className="self-start">
          <CardHeader>
            <CardTitle>
              <Trans>How it will be applied</Trans>
            </CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {!manual ? (
              <>
                {!preview ? (
                  <p className="text-sm text-muted-foreground" role="status">
                    {open.isFetching ? t`Calculating…` : t`Choose a unit and an amount.`}
                  </p>
                ) : (
                  <>
                    <Table aria-label={t`Automatic allocation`}>
                      <TableBody>
                        {preview.allocations.map((a) => (
                          <TableRow key={a.charge_id}>
                            <TableCell>{a.charge_description}</TableCell>
                            <TableCell className="text-end">
                              <Money minor={a.amount} currency={ctx.currency} />
                            </TableCell>
                          </TableRow>
                        ))}
                        {preview.credit > 0 ? (
                          <TableRow>
                            <TableCell>
                              <Badge variant="info">
                                <Trans>Kept as credit</Trans>
                              </Badge>
                            </TableCell>
                            <TableCell className="text-end">
                              <Money minor={preview.credit} currency={ctx.currency} />
                            </TableCell>
                          </TableRow>
                        ) : null}
                      </TableBody>
                    </Table>
                    <div>
                      <Button variant="outline" size="sm" onClick={startManual}>
                        <Trans>Choose charges myself</Trans>
                      </Button>
                    </div>
                  </>
                )}
              </>
            ) : (
              <>
                <p className="text-sm text-muted-foreground">
                  <Trans>Enter how much goes to each charge.</Trans>
                </p>
                {open.isPending ? (
                  <p role="status" className="text-sm">
                    <Trans>Loading charges…</Trans>
                  </p>
                ) : null}
                {rows.length === 0 && open.data?.length === 0 ? (
                  <p className="text-sm">
                    <Trans>This side has no open charges; the payment becomes credit.</Trans>
                  </p>
                ) : null}
                <Table aria-label={t`Manual allocation`}>
                  <TableBody>
                    {rows.map((r) => (
                      <TableRow key={r.charge_id}>
                        <TableCell>
                          {r.description}
                          <div className="text-xs text-muted-foreground">
                            <Trans>Open</Trans>: <Money minor={r.balance} currency={ctx.currency} />
                          </div>
                        </TableCell>
                        <TableCell className="w-40">
                          <MoneyInput
                            aria-label={`${t`Pay`} ${r.description ?? ''}`}
                            currency={ctx.currency}
                            value={r.amount}
                            onChange={(a) => setEdits((es) => ({ ...es, [r.charge_id]: a }))}
                          />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
                <p className="text-sm">
                  <Trans>
                    Allocated <Money minor={allocated} currency={ctx.currency} /> of{' '}
                    <Money minor={amount ?? 0n} currency={ctx.currency} />
                  </Trans>
                </p>
                {rowError ? (
                  <p role="alert" className="text-sm text-destructive">
                    <Trans>The amounts are more than the payment or a charge's balance.</Trans>
                  </p>
                ) : null}
                <div>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setManual(false)
                      setEdits({})
                    }}
                  >
                    <Trans>Back to automatic</Trans>
                  </Button>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

/* ───────────────────────── cash boxes, transfers, refunds ───────────────────────── */

export function CashBoxesPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const boxes = useCashBoxes(ctx)
  const transfers = useMoneyQuery(['transfers', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/transfers', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 50 } },
      }),
    ).then((r) => r.items),
  )
  const refunds = useMoneyQuery(['refunds', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/refunds', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 50 } },
      }),
    ).then((r) => r.items),
  )
  const accounts = useAccounts(ctx)
  const units = useAllUnits(ctx)
  const acctName = (id: string) => accounts.data?.find((a) => a.id === id)?.name ?? id
  const door = (id: string | null) => units.data?.find((u) => u.id === id)?.door_number ?? '–'
  const [transferFor, setTransferFor] = useState<{
    from: string
    kind: S['Transfer']['kind']
  } | null>(null)
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t`Cash boxes`}
        description={t`Each person who collects cash has a box. Handing cash over or depositing it at the bank is a transfer between accounts.`}
      />
      <QueryBoundary
        query={boxes}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No cash boxes yet`}
        emptyDescription={t`Add one for each person who collects cash.`}
      >
        {(items) => (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {items.map((b) => (
              <Card key={b.id}>
                <CardHeader>
                  <CardTitle>{b.name}</CardTitle>
                </CardHeader>
                <CardContent className="flex flex-col gap-3">
                  <div className="text-2xl font-semibold">
                    <Money minor={b.balance.amount} currency={ctx.currency} />
                  </div>
                  <div className="text-sm text-muted-foreground">
                    {b.collector_display_name ?? '–'}
                  </div>
                  <Gate permission="money.post" fallback={null}>
                    <div className="flex gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() =>
                          setTransferFor({ from: b.account_id, kind: 'cash_handover' })
                        }
                      >
                        <Trans>Hand over</Trans>
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setTransferFor({ from: b.account_id, kind: 'bank_deposit' })}
                      >
                        <Trans>Deposit to bank</Trans>
                      </Button>
                    </div>
                  </Gate>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </QueryBoundary>
      <Gate permission="money.manage" fallback={null}>
        <NewCashBox ctx={ctx} />
      </Gate>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Transfers</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <QueryBoundary
            query={transfers}
            isEmpty={(d) => d.length === 0}
            emptyTitle={t`No transfers yet`}
          >
            {(items) => (
              <Table>
                <TableBody>
                  {items.map((tr) => (
                    <TableRow key={tr.id}>
                      <TableCell>
                        <SiteDate value={tr.transferred_on} timeZone={ctx.timeZone} />
                      </TableCell>
                      <TableCell>
                        {acctName(tr.from_account_id)} → {acctName(tr.to_account_id)}
                      </TableCell>
                      <TableCell>
                        {tr.kind === 'cash_handover'
                          ? t`Handover`
                          : tr.kind === 'bank_deposit'
                            ? t`Bank deposit`
                            : t`Other`}
                      </TableCell>
                      <TableCell className="text-end">
                        <Money minor={tr.amount.amount} currency={ctx.currency} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </QueryBoundary>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Refunds</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <QueryBoundary query={refunds} isEmpty={(d) => d.length === 0} emptyTitle={t`No refunds`}>
            {(items) => (
              <Table>
                <TableBody>
                  {items.map((r) => (
                    <TableRow key={r.id}>
                      <TableCell>
                        <SiteDate value={r.refunded_on} timeZone={ctx.timeZone} />
                      </TableCell>
                      <TableCell>{door(r.unit_id)}</TableCell>
                      <TableCell>{r.reason}</TableCell>
                      <TableCell className="text-end">
                        <Money minor={r.amount.amount} currency={ctx.currency} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </QueryBoundary>
        </CardContent>
      </Card>
      {transferFor ? (
        <TransferDialog
          ctx={ctx}
          from={transferFor.from}
          kind={transferFor.kind}
          onClose={() => setTransferFor(null)}
        />
      ) : null}
    </div>
  )
}

function NewCashBox({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [name, setName] = useState('')
  const [person, setPerson] = useState('')
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async () => {
    await moneyApi.POST('/v1/orgs/{org_id}/cash-boxes', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: { site_id: ctx.siteId, name: name.trim(), collector_person_id: person || null },
    })
  })
  return (
    <form
      className="flex flex-wrap items-end gap-3"
      onSubmit={(e) => {
        e.preventDefault()
        if (!name.trim()) return setError(t`A name is required.`)
        setError(undefined)
        m.mutateAsync().then(
          () => {
            idem.renew()
            setName('')
            toast.info(t`Cash box added.`)
          },
          () => setError(t`Could not add the cash box.`),
        )
      }}
    >
      <Field label={t`New cash box`} htmlFor="cb-name" error={error}>
        <Input id="cb-name" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label={t`Collector`} htmlFor="cb-person">
        <PersonPicker ctx={ctx} id="cb-person" value={person} onChange={setPerson} />
      </Field>
      <Button type="submit" variant="outline" disabled={m.isPending}>
        <Trans>Add cash box</Trans>
      </Button>
    </form>
  )
}

function TransferDialog({
  ctx,
  from,
  kind,
  onClose,
}: {
  ctx: MoneyCtx
  from: string
  kind: S['Transfer']['kind']
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const bank = useAccounts(ctx, 'bank')
  const cash = useAccounts(ctx, 'cash_box')
  const targets =
    kind === 'bank_deposit' ? (bank.data ?? []) : (cash.data ?? []).filter((a) => a.id !== from)
  const [to, setTo] = useState('')
  const [amount, setAmount] = useState<bigint | null>(null)
  const [date, setDate] = useState(todayIn(ctx.timeZone))
  const [error, setError] = useState<string>()
  const target = to || targets[0]?.id || ''
  const m = useMoneyMutation(async () => {
    await moneyApi.POST('/v1/orgs/{org_id}/transfers', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: {
        site_id: ctx.siteId,
        from_account_id: from,
        to_account_id: target,
        amount: minorMoney(amount!, ctx.currency),
        ...toContractDate('transferred', date),
        kind,
      },
    })
  })
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            if (!amount || amount <= 0n) return setError(t`Enter an amount greater than zero.`)
            setError(undefined)
            m.mutateAsync().then(
              () => {
                idem.renew()
                toast.info(t`Transfer recorded.`)
                onClose()
              },
              (er) =>
                setError(
                  er instanceof AppError && transferShortfall(er)
                    ? t`The box does not hold that much.`
                    : er instanceof AppError && er.code === 'locked_period'
                      ? t`This period is locked.`
                      : er instanceof AppError && er.code === 'idempotency_mismatch'
                        ? t`That entry changed since the last try. Please check it and save again.`
                        : t`Could not record the transfer.`,
                ),
            )
          }}
        >
          <DialogHeader>
            <DialogTitle>
              {kind === 'bank_deposit' ? (
                <Trans>Deposit cash to the bank</Trans>
              ) : (
                <Trans>Hand cash over</Trans>
              )}
            </DialogTitle>
          </DialogHeader>
          <Field
            label={kind === 'bank_deposit' ? t`Bank account` : t`Hand over to`}
            htmlFor="tr-to"
          >
            <Select id="tr-to" value={target} onChange={(e) => setTo(e.target.value)}>
              {targets.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t`Amount`} htmlFor="tr-amount">
            <MoneyInput
              id="tr-amount"
              currency={ctx.currency}
              value={amount}
              onChange={setAmount}
            />
          </Field>
          <Field label={t`Date`} htmlFor="tr-date">
            <Input
              id="tr-date"
              type="date"
              dir="ltr"
              value={date}
              onChange={(e) => setDate(e.target.value)}
            />
          </Field>
          <FormRootError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={m.isPending}>
              <Trans>Record transfer</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
