import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { runWithReauth } from '@/app/reauth'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useIdempotencyKey } from '@/lib/idempotency'
import { problemMessage } from '@/lib/labels'
import { toast } from '@/lib/toast'
import {
  idemHeader,
  ifMatch,
  moneyApi,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
} from './api'
import { useAllUnits } from './pickers'
import {
  Field,
  FormRootError,
  Gate,
  PageHeader,
  QueryBoundary,
  Select,
  SideBadge,
  StatusBadge,
  Textarea,
} from './ui'

const STATUSES = ['pending', 'paid', 'failed', 'expired', 'refunded'] as const

/* ───────────────────────── my checkouts (resident) ───────────────────────── */

/** The card payments I started, newest first, each with its status. */
export function MyCheckoutsPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const q = useMoneyQuery(['my-checkouts'], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/me/checkouts', {
        params: { path: { org_id: ctx.orgId }, query: { limit: 200 } },
      }),
    ),
  )
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`My card payments`}
        description={t`The card payments you started, and where each one stands.`}
      />
      <QueryBoundary
        query={q}
        isEmpty={(d) => d.items.length === 0}
        emptyTitle={t`No card payments yet`}
        emptyDescription={t`When you pay by card, it shows up here.`}
      >
        {({ items, next_cursor }) => (
          <>
            {next_cursor ? (
              <p className="text-sm text-muted-foreground">
                <Trans>Showing the oldest 200 card payments.</Trans>
              </p>
            ) : null}
            <Table aria-label={t`My card payments`}>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <Trans>Started</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Side</Trans>
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
                {items
                  .slice()
                  .reverse()
                  .map((c) => (
                    <TableRow key={c.id}>
                      <TableCell>
                        <SiteDate value={c.created_at} timeZone={ctx.timeZone} kind="datetime" />
                      </TableCell>
                      <TableCell>
                        <SideBadge side={c.debtor_kind} />
                      </TableCell>
                      <TableCell className="text-end">
                        <Money minor={c.amount.amount} currency={c.amount.currency} />
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={c.status} />
                        {c.amount_mismatch ? (
                          <Badge variant="warning" className="ms-1">
                            <Trans>Amount differs</Trans>
                          </Badge>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-end">
                        <Link
                          to="/orgs/$orgId/pay/return"
                          params={{ orgId: ctx.orgId }}
                          search={{ checkout_id: c.id }}
                          className={buttonVariants({ variant: 'ghost', size: 'sm' })}
                        >
                          <Trans>Details</Trans>
                        </Link>
                      </TableCell>
                    </TableRow>
                  ))}
              </TableBody>
            </Table>
          </>
        )}
      </QueryBoundary>
    </div>
  )
}

/* ───────────────────────── checkouts of a site (manager) ───────────────────────── */

export function CheckoutsPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const [status, setStatus] = useState('')
  const units = useAllUnits(ctx)
  const door = (id: string) => units.data?.find((u) => u.id === id)?.door_number ?? '–'
  const q = useMoneyQuery(['checkouts', ctx.siteId, status], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/checkouts', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, status: status || undefined, limit: 200 },
        },
      }),
    ),
  )
  const labels: Record<string, string> = {
    pending: t`Pending`,
    paid: t`Paid`,
    failed: t`Failed`,
    expired: t`Expired`,
    refunded: t`Refunded`,
  }
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Card checkouts`}
        description={t`Every card payment residents started for this site. A payment that arrives late still counts: the checkout turns to paid.`}
        actions={
          <Link
            to="/orgs/$orgId/money/settlements"
            params={{ orgId: ctx.orgId }}
            className={buttonVariants({ variant: 'outline' })}
          >
            <Trans>Settlements</Trans>
          </Link>
        }
      />
      <div className="flex items-end gap-3">
        <Field label={t`Status`} htmlFor="co-status">
          <Select
            id="co-status"
            className="w-auto"
            value={status}
            onChange={(e) => setStatus(e.target.value)}
          >
            <option value="">{t`Any`}</option>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {labels[s]}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      <QueryBoundary
        query={q}
        isEmpty={(d) => d.items.length === 0}
        emptyTitle={status ? t`No checkouts with this status` : t`No card checkouts yet`}
        emptyDescription={status ? undefined : t`They appear here once a resident pays by card.`}
      >
        {({ items, next_cursor }) => (
          <>
            {next_cursor ? (
              <p className="text-sm text-muted-foreground">
                <Trans>
                  Showing the oldest 200 checkouts. Filter by status to narrow the list.
                </Trans>
              </p>
            ) : null}
            <Table aria-label={t`Card checkouts`}>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <Trans>Started</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Unit</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Side</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Provider</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Collected by</Trans>
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
                {items
                  .slice()
                  .reverse()
                  .map((c) => (
                    <TableRow key={c.id}>
                      <TableCell>
                        <SiteDate value={c.created_at} timeZone={ctx.timeZone} kind="datetime" />
                      </TableCell>
                      <TableCell>{door(c.unit_id)}</TableCell>
                      <TableCell>
                        <SideBadge side={c.debtor_kind} />
                      </TableCell>
                      <TableCell>{c.provider}</TableCell>
                      <TableCell>
                        {c.merchant_of_record === 'organization' ? (
                          <Trans>Organization</Trans>
                        ) : (
                          <Trans>Site</Trans>
                        )}
                      </TableCell>
                      <TableCell className="text-end">
                        <Money minor={c.amount.amount} currency={c.amount.currency} />
                        {c.amount_mismatch && c.confirmed_amount ? (
                          <div className="text-xs text-muted-foreground">
                            <Trans>
                              provider confirmed{' '}
                              <Money
                                minor={c.confirmed_amount.amount}
                                currency={c.confirmed_amount.currency}
                              />
                            </Trans>
                          </div>
                        ) : null}
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={c.status} />
                        {c.amount_mismatch ? (
                          <Badge variant="warning" className="ms-1">
                            <Trans>Amount differs</Trans>
                          </Badge>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-end whitespace-nowrap">
                        {c.status === 'paid' && c.payment_id ? (
                          <Gate permission="money.post" fallback={null}>
                            <RefundCheckoutButton ctx={ctx} checkout={c} />
                          </Gate>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  ))}
              </TableBody>
            </Table>
          </>
        )}
      </QueryBoundary>
    </div>
  )
}

/** Loads the payment behind a paid checkout (its `version` is the `If-Match`), then asks to refund. */
function RefundCheckoutButton({ ctx, checkout }: { ctx: MoneyCtx; checkout: S['Checkout'] }) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const [payment, setPayment] = useState<S['Payment'] | null>(null)
  const [busy, setBusy] = useState(false)
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        disabled={busy}
        onClick={() => {
          setBusy(true)
          qc.fetchQuery({
            queryKey: ['money', ctx.orgId, 'payment', checkout.payment_id],
            staleTime: 0,
            queryFn: () =>
              unwrap(
                moneyApi.GET('/v1/orgs/{org_id}/payments/{payment_id}', {
                  params: { path: { org_id: ctx.orgId, payment_id: checkout.payment_id! } },
                }),
              ),
          })
            .then(setPayment, () => toast.error(t`Could not load the payment.`))
            .finally(() => setBusy(false))
        }}
      >
        <Trans>Refund</Trans>
      </Button>
      {payment ? (
        <CardRefundDialog ctx={ctx} payment={payment} onClose={() => setPayment(null)} />
      ) : null}
    </>
  )
}

/* ───────────────────────── refund a provider payment ───────────────────────── */

/**
 * Refunds a card payment in full (the ledger has no partial refund): the provider refunds first,
 * then the reversal is posted. `If-Match` carries `payment.version`, the key follows the dialog,
 * and a re-authentication challenge is answered in place and replayed with the same key.
 */
export function CardRefundDialog({
  ctx,
  payment,
  onClose,
}: {
  ctx: MoneyCtx
  payment: S['Payment']
  onClose: () => void
}) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const { can } = useSession()
  const idem = useIdempotencyKey()
  const [reason, setReason] = useState('')
  const [error, setError] = useState<string>()
  const version = payment.version
  const refund = useMoneyMutation(async () => {
    await runWithReauth(() =>
      unwrap(
        moneyApi.POST('/v1/orgs/{org_id}/payments/{payment_id}:refund', {
          params: {
            path: { org_id: ctx.orgId, payment_id: payment.id },
            header: { ...idemHeader(idem.key), ...ifMatch(version ?? 0) },
          },
          body: reason.trim() ? { reason: reason.trim() } : {},
        }),
      ),
    )
  })
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            setError(undefined)
            refund.mutate(undefined, {
              onSuccess: () => {
                toast.info(t`The payment was refunded.`)
                onClose()
              },
              onError: (er) => {
                // a stale version or a half-done refund: show the current state before a retry
                void qc.invalidateQueries({ queryKey: ['money', ctx.orgId] })
                setError(
                  (er instanceof AppError
                    ? problemMessage(er.code, { _: (d) => t(d) })
                    : undefined) ??
                    (er instanceof AppError && er.status === 409
                      ? t`This payment cannot be refunded (it is not a confirmed card payment, or it changed). Reload and try again.`
                      : t`Could not refund the payment.`),
                )
              },
            })
          }}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>Refund this card payment?</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>
                The whole amount, <Money minor={payment.gross.amount} currency={ctx.currency} />,
                goes back to the resident&apos;s card through the provider. The charges it paid
                become open again. A partial refund is not possible.
              </Trans>
            </DialogDescription>
          </DialogHeader>
          <Field label={t`Reason (optional)`} htmlFor="refund-reason">
            <Textarea
              id="refund-reason"
              maxLength={500}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </Field>
          {version === undefined ? (
            <FormRootError message={t`The payment's version is missing. Reload the page.`} />
          ) : null}
          {!can('money.post') ? <FormRootError message={t`You may not refund payments.`} /> : null}
          <FormRootError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button
              type="submit"
              variant="destructive"
              disabled={refund.isPending || version === undefined}
            >
              <Trans>Refund the payment</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
