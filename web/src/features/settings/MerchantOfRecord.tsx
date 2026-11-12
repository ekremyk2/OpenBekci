import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { TriangleAlertIcon } from 'lucide-react'
import { draftApi, ifMatchOf, type Schema } from '@/api/draft'
import { AppError } from '@/api/errors'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { Money } from '@/components/data/Money'
import { Field, FormError, SubmitButton } from '@/components/forms/FormBits'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { problemMessage } from '@/lib/labels'
import { toast } from '@/lib/toast'

/** Org settings key (flat, merged key by key on update). */
export const MERCHANT_KEY = 'payments.merchant_of_record'

export interface OpenBalance {
  site_id: string
  account: string
  kind: string
  balance: number
}

/** The balances of a 409 `merchant_change_blocked` (`errors[].params.balances`), or null for any other error. */
export function blockedBalances(error: unknown): OpenBalance[] | null {
  if (!(error instanceof AppError) || error.code !== 'merchant_change_blocked') return null
  const out: OpenBalance[] = []
  for (const issues of Object.values(error.fieldErrors)) {
    for (const issue of issues) {
      const list = issue.params?.balances
      if (!Array.isArray(list)) continue
      for (const b of list) {
        if (b && typeof b === 'object' && typeof (b as OpenBalance).site_id === 'string')
          out.push(b as OpenBalance)
      }
    }
  }
  return out
}

/** Says what is still on its way and what to do about it, per site and per kind of money. */
export function MerchantBlockedExplanation({ balances }: { balances: OpenBalance[] }) {
  const { org } = useSession()
  const siteOf = (id: string) => org?.sites.find((s) => s.id === id)
  return (
    <div
      role="alert"
      className="flex flex-col gap-3 rounded-md border border-warning/40 bg-warning/10 p-4 text-sm"
    >
      <p className="flex items-center gap-2 font-medium">
        <TriangleAlertIcon className="size-4" aria-hidden="true" />
        <Trans>The merchant of record cannot change yet</Trans>
      </p>
      <p>
        <Trans>
          Money from card payments is still on its way, and switching now would leave it with the
          wrong account. Settle the following first, then try again.
        </Trans>
      </p>
      {balances.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {balances.map((b) => {
            const site = siteOf(b.site_id)
            const name = site?.name ?? b.site_id
            const currency = site?.currency ?? 'TRY'
            const abs = Math.abs(b.balance)
            const account = b.account
            return (
              <li key={`${b.site_id}:${b.account}`} data-testid="blocked-balance">
                {b.kind === 'client_money' ? (
                  <Trans>
                    <strong>{name}</strong>: the organization holds{' '}
                    <Money minor={abs} currency={currency} /> for the site as client money (account{' '}
                    {account}). Record the payout to the site, then try again.
                  </Trans>
                ) : (
                  <Trans>
                    <strong>{name}</strong>: the payment provider has not paid out{' '}
                    <Money minor={abs} currency={currency} /> yet (clearing account {account}). Wait
                    until the payout is posted.
                  </Trans>
                )}
              </li>
            )
          })}
        </ul>
      ) : null}
      {org ? (
        <p>
          <Link
            to="/orgs/$orgId/money/settlements"
            params={{ orgId: org.id }}
            className="underline underline-offset-4"
          >
            <Trans>Open the settlements and client money view</Trans>
          </Link>
        </p>
      ) : null}
    </div>
  )
}

function useBlockedMessage() {
  const { t } = useLingui()
  return (e: unknown) =>
    (e instanceof AppError ? problemMessage(e.code, { _: (d) => t(d) }) : undefined) ??
    (e instanceof AppError && e.status === 403
      ? t`Only an organization administrator can change this.`
      : undefined) ??
    t`Could not save the setting.`
}

/* ───────────────────────── organization default ───────────────────────── */

export function OrgMerchantCard({
  org,
  canManage,
}: {
  org: Schema<'Organization'>
  canManage: boolean
}) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const message = useBlockedMessage()
  const stored = org.settings[MERCHANT_KEY]
  const current = stored === 'organization' ? 'organization' : 'site'
  const [value, setValue] = useState<'site' | 'organization'>(current)
  const [blocked, setBlocked] = useState<OpenBalance[] | null>(null)
  const [error, setError] = useState<string>()
  const save = useMutation({
    mutationFn: (next: 'site' | 'organization') =>
      runWithReauth(async () => {
        const { error: e } = await draftApi.PATCH('/v1/orgs/{org_id}', {
          params: { path: { org_id: org.id }, header: ifMatchOf(org.version) },
          body: { settings: { [MERCHANT_KEY]: next } },
        })
        if (e) throw e
      }),
    onSuccess: async () => {
      setBlocked(null)
      setError(undefined)
      toast.info(t`Saved.`)
      await qc.invalidateQueries({ queryKey: ['tenancy.getOrg'] })
      await qc.invalidateQueries({ queryKey: ['tenancy.getSiteSettings'] })
    },
    onError: (e) => {
      const list = blockedBalances(e)
      setBlocked(list)
      setError(list ? undefined : message(e))
      // the stored value did not change: show it again
      setValue(current)
    },
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Card payments: who collects</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            By default each site collects card payments into its own account at the payment
            provider. A management company can instead collect centrally: the money reaches the
            company&apos;s bank account and is held as client money owed to each site until it is
            passed on. A single site can differ, see its own settings.
          </Trans>
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            setError(undefined)
            setBlocked(null)
            if (value !== current) save.mutate(value)
          }}
        >
          <Field id="org-merchant" label={<Trans>Collected by</Trans>}>
            <Select
              id="org-merchant"
              disabled={!canManage}
              value={value}
              onChange={(e) => setValue(e.target.value as 'site' | 'organization')}
            >
              <option value="site">{t`Each site, into its own account (default)`}</option>
              <option value="organization">{t`The organization, centrally (client money)`}</option>
            </Select>
          </Field>
          {blocked ? <MerchantBlockedExplanation balances={blocked} /> : null}
          <FormError message={error} />
          {canManage ? (
            <SubmitButton busy={save.isPending} disabled={value === current} className="w-fit">
              <Trans>Save</Trans>
            </SubmitButton>
          ) : (
            <p className="text-sm text-muted-foreground">
              <Trans>Only an organization administrator can change this.</Trans>
            </p>
          )}
        </form>
      </CardContent>
    </Card>
  )
}

/* ───────────────────────── per-site override ───────────────────────── */

type Override = '' | 'site' | 'organization'

export function SiteMerchantCard({
  orgId,
  siteId,
  settings,
}: {
  orgId: string
  siteId: string
  settings: Schema<'SiteSettings'>
}) {
  const { t } = useLingui()
  const { can } = useSession()
  const qc = useQueryClient()
  const message = useBlockedMessage()
  const canChange = can('org.manage')
  const payments = settings.payments
  const stored: Override = payments?.merchant_of_record_override ?? ''
  const effective = payments?.merchant_of_record ?? 'site'
  const [value, setValue] = useState<Override>(stored)
  const [blocked, setBlocked] = useState<OpenBalance[] | null>(null)
  const [error, setError] = useState<string>()
  const save = useMutation({
    mutationFn: (next: Override) =>
      runWithReauth(async () => {
        const { error: e } = await draftApi.PATCH('/v1/orgs/{org_id}/sites/{site_id}/settings', {
          params: {
            path: { org_id: orgId, site_id: siteId },
            header: ifMatchOf(settings.version),
          },
          body: { merchant_of_record: next === '' ? null : next },
        })
        if (e) throw e
      }),
    onSuccess: async () => {
      setBlocked(null)
      setError(undefined)
      toast.info(t`Saved.`)
      await Promise.all([
        qc.invalidateQueries({ queryKey: ['tenancy.getSiteSettings', orgId, siteId] }),
        qc.invalidateQueries({ queryKey: ['tenancy.getSite', orgId] }),
        qc.invalidateQueries({ queryKey: ['tenancy.getBankDetails', orgId, siteId] }),
      ])
    },
    onError: (e) => {
      const list = blockedBalances(e)
      setBlocked(list)
      setError(list ? undefined : message(e))
      setValue(stored)
    },
  })
  return (
    <section aria-labelledby="site-merchant-title" className="flex flex-col gap-3">
      <h3 id="site-merchant-title" className="text-sm font-semibold">
        <Trans>Card payments: who collects</Trans>
      </h3>
      <p className="text-sm text-muted-foreground">
        {effective === 'organization' ? (
          <Trans>
            Right now the organization collects this site&apos;s card payments centrally and holds
            the money as client money until it is passed on.
          </Trans>
        ) : (
          <Trans>
            Right now this site collects its own card payments into its provider account.
          </Trans>
        )}
      </p>
      <form
        className="flex flex-col gap-3"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          setError(undefined)
          setBlocked(null)
          if (value !== stored) save.mutate(value)
        }}
      >
        <Field id="site-merchant" label={<Trans>For this site</Trans>}>
          <Select
            id="site-merchant"
            disabled={!canChange}
            value={value}
            onChange={(e) => setValue(e.target.value as Override)}
          >
            <option value="">{t`Follow the organization`}</option>
            <option value="site">{t`This site collects into its own account`}</option>
            <option value="organization">{t`The organization collects centrally`}</option>
          </Select>
        </Field>
        {blocked ? <MerchantBlockedExplanation balances={blocked} /> : null}
        <FormError message={error} />
        {canChange ? (
          <SubmitButton busy={save.isPending} disabled={value === stored} className="w-fit">
            <Trans>Save</Trans>
          </SubmitButton>
        ) : (
          <p className="text-sm text-muted-foreground">
            <Trans>Only an organization administrator can change this.</Trans>
          </p>
        )}
      </form>
    </section>
  )
}
