import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import {
  CircleAlertIcon,
  CircleCheckIcon,
  CircleXIcon,
  ClockIcon,
  LoaderCircleIcon,
  Undo2Icon,
} from 'lucide-react'
import { downloadFile } from '@/api/download'
import { AppError } from '@/api/errors'
import { Money } from '@/components/data/Money'
import { EmptyState } from '@/components/data/EmptyState'
import { buttonVariants, Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { toast } from '@/lib/toast'
import type { S } from './api'
import { usePollCheckout } from './checkout'
import { PageHeader } from './ui'

/**
 * Where the provider sends the payer back: `/orgs/$orgId/pay/return?checkout_id=`. Polls the
 * checkout with backoff until it is final, then says plainly what happened.
 */
export function PayReturnPage({ orgId, checkoutId }: { orgId: string; checkoutId?: string }) {
  const { t } = useLingui()
  const poll = usePollCheckout(orgId, checkoutId ?? '')
  const c = poll.checkout
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={t`Card payment`} />
      {!checkoutId ? (
        <EmptyState
          icon={CircleAlertIcon}
          title={t`This page needs a payment to look up`}
          description={t`Open it from the link the payment provider sent you back to, or start again from Pay.`}
          action={<PayAgain orgId={orgId} />}
        />
      ) : poll.error && !c ? (
        <EmptyState
          icon={CircleAlertIcon}
          title={
            poll.error instanceof AppError && poll.error.status === 404
              ? t`We could not find this payment`
              : t`We could not check your payment`
          }
          description={t`Nothing was lost. Check your statement or your notifications, or try again.`}
          action={
            <Button variant="outline" onClick={poll.recheck}>
              <Trans>Check again</Trans>
            </Button>
          }
        />
      ) : !c ? (
        <Waiting />
      ) : (
        <Outcome orgId={orgId} checkout={c} stalled={poll.stalled} recheck={poll.recheck} />
      )}
    </div>
  )
}

function PayAgain({ orgId, retry }: { orgId: string; retry?: boolean }) {
  return (
    <Link to="/orgs/$orgId/money/pay" params={{ orgId }} className={buttonVariants()}>
      {retry ? <Trans>Try again</Trans> : <Trans>Back to Pay</Trans>}
    </Link>
  )
}

function Waiting() {
  return (
    <Card>
      <CardContent className="flex items-center gap-3 py-6" role="status">
        <LoaderCircleIcon className="animate-spin" aria-hidden="true" />
        <span>
          <Trans>
            Confirming your payment with the provider. This usually takes a few seconds.
          </Trans>
        </span>
      </CardContent>
    </Card>
  )
}

function Outcome({
  orgId,
  checkout: c,
  stalled,
  recheck,
}: {
  orgId: string
  checkout: S['Checkout']
  stalled: boolean
  recheck: () => void
}) {
  const { t } = useLingui()
  const currency = c.amount.currency
  if (c.status === 'pending') {
    return stalled ? (
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ClockIcon aria-hidden="true" /> <Trans>Still waiting for the provider</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3 text-sm">
          <p>
            <Trans>
              The payment window has closed and the provider has not confirmed a payment yet. If
              your card was charged, it can still show up in a few minutes and you will get a
              notification with the receipt.
            </Trans>
          </p>
          <div className="flex gap-2">
            <Button variant="outline" onClick={recheck}>
              <Trans>Check again</Trans>
            </Button>
            <PayAgain orgId={orgId} />
          </div>
        </CardContent>
      </Card>
    ) : (
      <Waiting />
    )
  }
  if (c.status === 'paid') {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2" role="status">
            <CircleCheckIcon className="text-green-600" aria-hidden="true" />{' '}
            <Trans>Payment received</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3 text-sm">
          <p>
            <Trans>
              Thank you. We received{' '}
              <Money
                minor={
                  (c.amount_mismatch && c.confirmed_amount ? c.confirmed_amount : c.amount).amount
                }
                currency={currency}
              />{' '}
              by card.
            </Trans>
          </p>
          {c.amount_mismatch ? (
            <p role="alert" className="rounded-md bg-amber-500/10 p-3">
              {c.confirmed_amount ? (
                <Trans>
                  The provider confirmed{' '}
                  <Money minor={c.confirmed_amount.amount} currency={currency} /> instead of the{' '}
                  <Money minor={c.amount.amount} currency={currency} /> you chose. Your payment was
                  recorded for the amount the provider confirmed. If this looks wrong, contact your
                  manager.
                </Trans>
              ) : (
                <Trans>
                  The provider confirmed a different amount than the one you chose. Your payment was
                  recorded for the amount the provider confirmed. If this looks wrong, contact your
                  manager.
                </Trans>
              )}
            </p>
          ) : null}
          <div className="flex flex-wrap gap-2">
            {c.payment_id ? (
              <Button
                onClick={() =>
                  downloadFile(
                    {
                      endpoint: '/v1/orgs/{org_id}/payments/{payment_id}/receipt',
                      path: { org_id: orgId, payment_id: c.payment_id! },
                    },
                    `receipt-${c.payment_id}.pdf`,
                  ).catch(() => toast.error(t`The download failed. Please try again.`))
                }
              >
                <Trans>Download receipt (PDF)</Trans>
              </Button>
            ) : null}
            <Link
              to="/orgs/$orgId/money/statements"
              params={{ orgId }}
              search={{ unit: c.unit_id, side: c.debtor_kind }}
              className={buttonVariants({ variant: 'outline' })}
            >
              <Trans>See my statement</Trans>
            </Link>
          </div>
        </CardContent>
      </Card>
    )
  }
  if (c.status === 'refunded') {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2" role="status">
            <Undo2Icon aria-hidden="true" /> <Trans>This payment was refunded</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent className="text-sm">
          <Trans>
            <Money minor={c.amount.amount} currency={currency} /> went back to your card.
          </Trans>
        </CardContent>
      </Card>
    )
  }
  const failed = c.status === 'failed'
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2" role="status">
          {failed ? (
            <CircleXIcon className="text-destructive" aria-hidden="true" />
          ) : (
            <ClockIcon aria-hidden="true" />
          )}
          {failed ? (
            <Trans>The payment did not go through</Trans>
          ) : (
            <Trans>The payment page expired</Trans>
          )}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        <p>
          {failed ? (
            c.failure_code === 'provider_declined' ? (
              <Trans>Your card was declined. Nothing was charged.</Trans>
            ) : (
              <Trans>The provider could not complete the payment. Nothing was charged.</Trans>
            )
          ) : (
            <Trans>The payment was not completed in time. Nothing was charged.</Trans>
          )}
        </p>
        <div>
          <PayAgain orgId={orgId} retry />
        </div>
      </CardContent>
    </Card>
  )
}
