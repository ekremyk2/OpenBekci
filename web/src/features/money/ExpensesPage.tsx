import { useEffect, useMemo, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm, useWatch } from 'react-hook-form'
import { Trans, useLingui } from '@lingui/react/macro'
import { z } from 'zod'
import { AppError } from '@/api/errors'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { MoneyInput } from '@/components/forms/MoneyInput'
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
import { isoDate, MoneyFormField, positiveMinor, useZodMessages } from './forms'
import {
  AccountPicker,
  CashOrBankPicker,
  FundPicker,
  VendorPicker,
  useAccounts,
  useFunds,
  useVendors,
} from './pickers'
import {
  applyServerErrors,
  Checkbox,
  Field,
  FormRootError,
  Gate,
  PageHeader,
  QueryBoundary,
  ReasonDialog,
  Select,
  StatusBadge,
} from './ui'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'

const BILLS_KEY = 'ob.money.bills'
const billsEnabled = (siteId: string) => {
  try {
    return localStorage.getItem(`${BILLS_KEY}.${siteId}`) === '1'
  } catch {
    return false
  }
}

export function ExpensesPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const [category, setCategory] = useState('')
  const [fund, setFund] = useState('')
  const [text, setText] = useState('')
  const [status, setStatus] = useState('')
  const [adding, setAdding] = useState(false)
  const [voidFor, setVoidFor] = useState<S['Expense'] | null>(null)
  const [bills, setBills] = useState(billsEnabled(ctx.siteId))
  const cats = useAccounts(ctx, 'expense_category')
  const q = useMoneyQuery(['expenses', ctx.siteId, category, fund, text, status], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/expenses', {
        params: {
          path: { org_id: ctx.orgId },
          query: {
            site_id: ctx.siteId,
            category_account_id: category || undefined,
            fund_id: fund || undefined,
            q: text || undefined,
            status: status || undefined,
            limit: 200,
          },
        },
      }),
    ).then((r) => r.items),
  )
  const catName = (id: string) => cats.data?.find((c) => c.id === id)?.name ?? id
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t`Expenses`}
        description={t`What was spent, who was paid and from which account. Approval happens in your meetings; this records the result.`}
        actions={
          <Gate permission="money.post" fallback={null}>
            <Button onClick={() => setAdding(true)}>
              <Trans>New expense</Trans>
            </Button>
          </Gate>
        }
      />
      <div className="grid gap-3 sm:grid-cols-4">
        <Field label={t`Category`} htmlFor="ef-cat">
          <AccountPicker
            ctx={ctx}
            subtype="expense_category"
            id="ef-cat"
            value={category}
            onChange={setCategory}
            placeholder={t`All categories`}
          />
        </Field>
        <Field label={t`Fund`} htmlFor="ef-fund">
          <FundPicker
            ctx={ctx}
            id="ef-fund"
            value={fund}
            onChange={setFund}
            placeholder={t`All funds`}
          />
        </Field>
        <Field label={t`Search`} htmlFor="ef-q">
          <Input id="ef-q" type="search" value={text} onChange={(e) => setText(e.target.value)} />
        </Field>
        <Field label={t`Status`} htmlFor="ef-status">
          <Select id="ef-status" value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="">{t`Any`}</option>
            <option value="posted">{t`Posted`}</option>
            <option value="voided">{t`Voided`}</option>
          </Select>
        </Field>
      </div>
      <QueryBoundary
        query={q}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No expenses`}
        emptyDescription={t`Record what the site spends: electricity, cleaning, repairs.`}
      >
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Date</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Description</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Category</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Vendor</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Document</Trans>
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
              {items.map((x) => (
                <TableRow
                  key={x.id}
                  className={x.status === 'voided' ? 'text-muted-foreground' : undefined}
                >
                  <TableCell>
                    <SiteDate value={x.paid_on} timeZone={ctx.timeZone} />
                  </TableCell>
                  <TableCell>{x.description}</TableCell>
                  <TableCell>{catName(x.category_account_id)}</TableCell>
                  <TableCell>{x.vendor_name ?? '–'}</TableCell>
                  <TableCell>{x.document_no ?? '–'}</TableCell>
                  <TableCell className="text-end">
                    <Money minor={x.amount.amount} currency={ctx.currency} />
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={x.status} />
                  </TableCell>
                  <TableCell className="text-end">
                    {x.status === 'posted' ? (
                      <Gate permission="money.post" fallback={null}>
                        <Button size="sm" variant="ghost" onClick={() => setVoidFor(x)}>
                          <Trans>Void</Trans>
                        </Button>
                      </Gate>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>
      <RecurringCard ctx={ctx} />
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Vendor bills</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <Checkbox
            label={t`Track vendor bills with due dates and partial payments`}
            checked={bills}
            onChange={(e) => {
              setBills(e.target.checked)
              try {
                localStorage.setItem(`${BILLS_KEY}.${ctx.siteId}`, e.target.checked ? '1' : '0')
              } catch {
                /* ignore */
              }
            }}
          />
          {bills ? (
            <BillsSection ctx={ctx} />
          ) : (
            <p className="text-sm text-muted-foreground">
              <Trans>Off: expenses are recorded as paid when they happen.</Trans>
            </p>
          )}
        </CardContent>
      </Card>
      {adding ? <NewExpenseDialog ctx={ctx} onClose={() => setAdding(false)} /> : null}
      {voidFor ? (
        <VoidExpense ctx={ctx} expense={voidFor} onClose={() => setVoidFor(null)} />
      ) : null}
    </div>
  )
}

function VoidExpense({
  ctx,
  expense,
  onClose,
}: {
  ctx: MoneyCtx
  expense: S['Expense']
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async (reason: string) => {
    await moneyApi.POST('/v1/orgs/{org_id}/expenses/{expense_id}:void', {
      params: { path: { org_id: ctx.orgId, expense_id: expense.id }, header: idemHeader(idem.key) },
      body: { reason },
    })
  })
  return (
    <ReasonDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t`Void this expense?`}
      description={expense.description}
      confirmLabel={t`Void expense`}
      destructive
      pending={m.isPending}
      error={error}
      onConfirm={(reason) =>
        m
          .mutateAsync(reason)
          .then(onClose, (e) =>
            setError(
              e instanceof AppError && e.code === 'locked_period'
                ? t`This period is locked.`
                : t`Could not void the expense.`,
            ),
          )
      }
    />
  )
}

/** Uploads a receipt photo through the files endpoint and returns its id. */
async function uploadReceipt(ctx: MoneyCtx, file: File, key: string): Promise<string> {
  const res = await moneyApi.POST('/v1/orgs/{org_id}/files', {
    params: { path: { org_id: ctx.orgId }, header: { 'Idempotency-Key': key } },
    body: { file: file as unknown as string, site_id: ctx.siteId, scope: 'expense_receipt' },
    bodySerializer: () => {
      const fd = new FormData()
      fd.set('file', file)
      fd.set('site_id', ctx.siteId)
      fd.set('scope', 'expense_receipt')
      return fd
    },
  })
  return res.data!.id
}

function NewExpenseDialog({ ctx, onClose }: { ctx: MoneyCtx; onClose: () => void }) {
  const { t } = useLingui()
  const msg = useZodMessages()
  const idem = useIdempotencyKey()
  const funds = useFunds(ctx)
  const [photo, setPhoto] = useState<File | null>(null)
  const [recurring, setRecurring] = useState(false)
  const schema = useMemo(
    () =>
      z.object({
        category_account_id: z.string().min(1, msg.pick),
        fund_id: z.string().min(1, msg.pick),
        amount: positiveMinor(msg.amount),
        paid_at: isoDate(msg.date),
        paid_from_account_id: z.string().min(1, msg.pick),
        vendor_id: z.string(),
        description: z.string().trim().min(1, msg.required),
        document_no: z.string(),
      }),
    [msg],
  )
  type V = z.infer<typeof schema>
  const form = useForm<V>({
    resolver: zodResolver(schema),
    defaultValues: {
      category_account_id: '',
      fund_id: '',
      amount: null as unknown as bigint,
      paid_at: todayIn(ctx.timeZone),
      paid_from_account_id: '',
      vendor_id: '',
      description: '',
      document_no: '',
    },
  })
  const v = useWatch({ control: form.control })
  const defaultFund = funds.data?.find((f) => f.is_default)?.id
  useEffect(() => {
    if (defaultFund && !form.getValues('fund_id')) form.setValue('fund_id', defaultFund)
  }, [defaultFund, form])
  const fund = v.fund_id || defaultFund || ''
  const create = useMoneyMutation(async (values: V) => {
    const fileId = photo ? await uploadReceipt(ctx, photo, crypto.randomUUID()) : null
    const res = await moneyApi.POST('/v1/orgs/{org_id}/expenses', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: {
        site_id: ctx.siteId,
        category_account_id: values.category_account_id,
        fund_id: fund,
        amount: minorMoney(values.amount, ctx.currency),
        paid_on: values.paid_at,
        paid_from_account_id: values.paid_from_account_id,
        vendor_id: values.vendor_id || null,
        description: values.description,
        document_no: values.document_no || null,
        file_id: fileId,
      },
    })
    if (recurring) {
      const next = new Date(
        Date.UTC(
          Number(values.paid_at.slice(0, 4)),
          Number(values.paid_at.slice(5, 7)),
          Math.min(28, Number(values.paid_at.slice(8, 10))),
        ),
      )
      await moneyApi.POST('/v1/orgs/{org_id}/recurring-expenses', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(crypto.randomUUID()) },
        body: {
          site_id: ctx.siteId,
          category_account_id: values.category_account_id,
          fund_id: fund,
          amount: minorMoney(values.amount, ctx.currency),
          paid_from_account_id: values.paid_from_account_id,
          vendor_id: values.vendor_id || null,
          description: values.description,
          day_of_month: Math.min(28, Number(values.paid_at.slice(8, 10))),
          start: next.toISOString().slice(0, 10),
        },
      })
    }
    return res.data!
  })
  const err = form.formState.errors
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            <Trans>New expense</Trans>
          </DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(e) =>
            void form.handleSubmit(async (values) => {
              try {
                await create.mutateAsync(values)
                idem.renew()
                toast.info(t`Expense recorded.`)
                onClose()
              } catch (er) {
                applyServerErrors(form, er, t`Could not record the expense.`, {
                  locked_period: t`This period is locked.`,
                })
              }
            })(e)
          }
        >
          <Field label={t`What was it for?`} htmlFor="ex-desc" error={err.description?.message}>
            <Input id="ex-desc" {...form.register('description')} />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t`Category`} htmlFor="ex-cat" error={err.category_account_id?.message}>
              <AccountPicker
                ctx={ctx}
                subtype="expense_category"
                id="ex-cat"
                value={v.category_account_id ?? ''}
                onChange={(id) =>
                  form.setValue('category_account_id', id, { shouldValidate: true })
                }
              />
            </Field>
            <Field label={t`Fund`} htmlFor="ex-fund" error={err.fund_id?.message}>
              <FundPicker
                ctx={ctx}
                id="ex-fund"
                value={fund}
                onChange={(id) => form.setValue('fund_id', id, { shouldValidate: true })}
              />
            </Field>
            <MoneyFormField
              id="ex-amount"
              control={form.control}
              name="amount"
              label={t`Amount`}
              currency={ctx.currency}
              error={err.amount?.message}
            />
            <Field label={t`Paid on`} htmlFor="ex-date" error={err.paid_at?.message}>
              <Input id="ex-date" type="date" dir="ltr" {...form.register('paid_at')} />
            </Field>
            <Field label={t`Paid from`} htmlFor="ex-from" error={err.paid_from_account_id?.message}>
              <CashOrBankPicker
                ctx={ctx}
                id="ex-from"
                value={v.paid_from_account_id ?? ''}
                onChange={(id) =>
                  form.setValue('paid_from_account_id', id, { shouldValidate: true })
                }
              />
            </Field>
            <Field label={t`Vendor (optional)`} htmlFor="ex-vendor">
              <VendorPicker
                ctx={ctx}
                id="ex-vendor"
                value={v.vendor_id ?? ''}
                onChange={(id) => form.setValue('vendor_id', id)}
              />
            </Field>
          </div>
          <Field label={t`Document number (optional)`} htmlFor="ex-doc">
            <Input id="ex-doc" {...form.register('document_no')} />
          </Field>
          <Field label={t`Receipt photo (optional)`} htmlFor="ex-photo">
            <Input
              id="ex-photo"
              type="file"
              accept="image/*,application/pdf"
              onChange={(e) => setPhoto(e.target.files?.[0] ?? null)}
            />
          </Field>
          <Checkbox
            label={t`Repeat every month`}
            checked={recurring}
            onChange={(e) => setRecurring(e.target.checked)}
          />
          <FormRootError message={err.root?.message} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={form.formState.isSubmitting}>
              <Trans>Save expense</Trans>
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RecurringCard({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const q = useMoneyQuery(['recurring', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/recurring-expenses', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, status: 'active' } },
      }),
    ).then((r) => r.items),
  )
  const end = useMoneyMutation(async (r: S['RecurringExpense']) => {
    await moneyApi.DELETE('/v1/orgs/{org_id}/recurring-expenses/{recurring_expense_id}', {
      params: {
        path: { org_id: ctx.orgId, recurring_expense_id: r.id },
        header: ifMatch(r.version),
      },
    })
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Monthly recurring expenses</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <QueryBoundary
          query={q}
          isEmpty={(d) => d.length === 0}
          emptyTitle={t`None`}
          emptyDescription={t`Tick “Repeat every month” on a new expense to add one.`}
        >
          {(items) => (
            <Table>
              <TableBody>
                {items.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell>{r.description}</TableCell>
                    <TableCell>
                      <Trans>Day {r.day_of_month}</Trans>
                    </TableCell>
                    <TableCell>
                      {r.next_run_on ? (
                        <SiteDate value={r.next_run_on} timeZone={ctx.timeZone} />
                      ) : (
                        '–'
                      )}
                    </TableCell>
                    <TableCell className="text-end">
                      <Money minor={r.amount.amount} currency={ctx.currency} />
                    </TableCell>
                    <TableCell className="text-end">
                      <Gate permission="money.post" fallback={null}>
                        <Button size="sm" variant="ghost" onClick={() => end.mutate(r)}>
                          <Trans>Stop repeating</Trans>
                        </Button>
                      </Gate>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </QueryBoundary>
      </CardContent>
    </Card>
  )
}

function BillsSection({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const vendors = useVendors(ctx)
  const [paying, setPaying] = useState<S['Bill'] | null>(null)
  const [creating, setCreating] = useState(false)
  const q = useMoneyQuery(['bills', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/bills', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items),
  )
  const vendor = (id: string) => vendors.data?.find((v) => v.id === id)?.name ?? id
  return (
    <div className="flex flex-col gap-3">
      <div>
        <Gate permission="money.post" fallback={null}>
          <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
            <Trans>New bill</Trans>
          </Button>
        </Gate>
      </div>
      <QueryBoundary query={q} isEmpty={(d) => d.length === 0} emptyTitle={t`No bills`}>
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Vendor</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Due</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>Amount</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>Open</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Status</Trans>
                </TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((b) => (
                <TableRow key={b.id}>
                  <TableCell>{vendor(b.vendor_id)}</TableCell>
                  <TableCell>
                    <SiteDate value={b.due_date} timeZone={ctx.timeZone} />
                  </TableCell>
                  <TableCell className="text-end">
                    <Money minor={b.amount.amount} currency={ctx.currency} />
                  </TableCell>
                  <TableCell className="text-end">
                    <Money minor={b.balance.amount} currency={ctx.currency} />
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={b.status} />
                  </TableCell>
                  <TableCell className="text-end">
                    {b.balance.amount > 0 && b.status !== 'cancelled' ? (
                      <Gate permission="money.post" fallback={null}>
                        <Button size="sm" variant="ghost" onClick={() => setPaying(b)}>
                          <Trans>Pay</Trans>
                        </Button>
                      </Gate>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>
      {creating ? <NewBill ctx={ctx} onClose={() => setCreating(false)} /> : null}
      {paying ? <PayBill ctx={ctx} bill={paying} onClose={() => setPaying(null)} /> : null}
    </div>
  )
}

function NewBill({ ctx, onClose }: { ctx: MoneyCtx; onClose: () => void }) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const funds = useFunds(ctx)
  const [vendor, setVendor] = useState('')
  const [cat, setCat] = useState('')
  const [amount, setAmount] = useState<bigint | null>(null)
  const [issue, setIssue] = useState(todayIn(ctx.timeZone))
  const [due, setDue] = useState(todayIn(ctx.timeZone))
  const [doc, setDoc] = useState('')
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async () => {
    await moneyApi.POST('/v1/orgs/{org_id}/bills', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: {
        site_id: ctx.siteId,
        vendor_id: vendor,
        document_no: doc || null,
        issue_date: issue,
        due_date: due,
        amount: minorMoney(amount!, ctx.currency),
        category_account_id: cat,
        fund_id: funds.data?.find((f) => f.is_default)?.id ?? '',
      },
    })
  })
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>New vendor bill</Trans>
          </DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            if (!vendor || !cat || !amount || amount <= 0n)
              return setError(t`Fill in the vendor, category and amount.`)
            m.mutateAsync().then(
              () => {
                idem.renew()
                onClose()
              },
              () => setError(t`Could not save the bill.`),
            )
          }}
        >
          <Field label={t`Vendor`} htmlFor="b-vendor">
            <VendorPicker ctx={ctx} id="b-vendor" value={vendor} onChange={setVendor} />
          </Field>
          <Field label={t`Category`} htmlFor="b-cat">
            <AccountPicker
              ctx={ctx}
              subtype="expense_category"
              id="b-cat"
              value={cat}
              onChange={setCat}
            />
          </Field>
          <Field label={t`Amount`} htmlFor="b-amount">
            <MoneyInput id="b-amount" currency={ctx.currency} value={amount} onChange={setAmount} />
          </Field>
          <div className="grid grid-cols-2 gap-4">
            <Field label={t`Issued`} htmlFor="b-issue">
              <Input
                id="b-issue"
                type="date"
                dir="ltr"
                value={issue}
                onChange={(e) => setIssue(e.target.value)}
              />
            </Field>
            <Field label={t`Due`} htmlFor="b-due">
              <Input
                id="b-due"
                type="date"
                dir="ltr"
                value={due}
                onChange={(e) => setDue(e.target.value)}
              />
            </Field>
          </div>
          <Field label={t`Document number (optional)`} htmlFor="b-doc">
            <Input id="b-doc" value={doc} onChange={(e) => setDoc(e.target.value)} />
          </Field>
          <FormRootError message={error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={m.isPending}>
              <Trans>Save bill</Trans>
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function PayBill({ ctx, bill, onClose }: { ctx: MoneyCtx; bill: S['Bill']; onClose: () => void }) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [amount, setAmount] = useState<bigint | null>(BigInt(bill.balance.amount))
  const [from, setFrom] = useState('')
  const [date, setDate] = useState(todayIn(ctx.timeZone))
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async () => {
    await moneyApi.POST('/v1/orgs/{org_id}/bills/{bill_id}/payments', {
      params: { path: { org_id: ctx.orgId, bill_id: bill.id }, header: idemHeader(idem.key) },
      body: {
        amount: minorMoney(amount!, ctx.currency),
        paid_on: date,
        paid_from_account_id: from,
      },
    })
  })
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>Pay bill</Trans>
          </DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            if (!amount || amount <= 0n || amount > BigInt(bill.balance.amount) || !from)
              return setError(t`Enter an amount up to the open balance and the account.`)
            m.mutateAsync().then(
              () => {
                idem.renew()
                onClose()
              },
              () => setError(t`Could not record the payment.`),
            )
          }}
        >
          <Field label={t`Amount (partial payments are fine)`} htmlFor="bp-amount">
            <MoneyInput
              id="bp-amount"
              currency={ctx.currency}
              value={amount}
              onChange={setAmount}
            />
          </Field>
          <Field label={t`Paid from`} htmlFor="bp-from">
            <CashOrBankPicker ctx={ctx} id="bp-from" value={from} onChange={setFrom} />
          </Field>
          <Field label={t`Paid on`} htmlFor="bp-date">
            <Input
              id="bp-date"
              type="date"
              dir="ltr"
              value={date}
              onChange={(e) => setDate(e.target.value)}
            />
          </Field>
          <FormRootError message={error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={m.isPending}>
              <Trans>Record payment</Trans>
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
