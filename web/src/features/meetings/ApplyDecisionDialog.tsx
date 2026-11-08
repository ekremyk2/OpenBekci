import { useMemo, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { runWithReauthUnlessCancelled as run } from '@/app/reauth'
import { MoneyInput } from '@/components/forms/MoneyInput'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useBodyIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import { usePersons } from './hooks'
import {
  gApi,
  idemHeader,
  unwrap,
  useOrgMutation,
  useOrgQuery,
  type S,
  type SiteCtx,
} from './kit/api'
import { addDays, todayIn } from './kit/time'
import { LabeledCheckbox, Field, FormRootError, Select } from './kit/ui'
import { Callout } from './kit/widgets'
import { ELECTED_ROLES, useGovLabels } from './labels'

type Kind = S['DecisionEffect']['kind']
const KINDS: Kind[] = ['dues_rate', 'membership', 'budget_final', 'special_assessment']

interface FormState {
  planId: string
  from: string
  amount: bigint | null
  personId: string
  role: string
  validFrom: string
  validTo: string
  replacesMembershipIds: string[]
  budgetId: string
  saName: string
  saAmount: bigint | null
  saStart: string
  keyId: string
}

/** Builds the request body of a kind from the form state; null while required values are missing. */
export function buildApplyBody(
  kind: Kind,
  f: FormState,
  ctx: Pick<SiteCtx, 'currency' | 'siteId'>,
): S['DecisionApply'] | null {
  switch (kind) {
    case 'dues_rate':
      if (!f.planId || !f.from || f.amount === null || f.amount <= 0n) return null
      return {
        kind,
        plan_id: f.planId,
        from: f.from,
        amount: { amount: Number(f.amount), currency: ctx.currency },
      }
    case 'membership':
      if (!f.personId || !f.role || !f.validFrom || !f.validTo || f.validTo <= f.validFrom)
        return null
      return {
        kind,
        person_id: f.personId,
        role: f.role,
        scope_type: 'site',
        scope_id: ctx.siteId,
        valid_from: f.validFrom,
        valid_to: f.validTo,
        ...(f.replacesMembershipIds.length
          ? { replaces_membership_ids: f.replacesMembershipIds }
          : {}),
      }
    case 'budget_final':
      if (!f.budgetId) return null
      return { kind, budget_id: f.budgetId }
    case 'special_assessment':
      if (!f.saName.trim() || f.saAmount === null || f.saAmount <= 0n || !f.saStart || !f.keyId)
        return null
      return {
        kind,
        plan: {
          site_id: ctx.siteId,
          name: f.saName.trim(),
          kind: 'one_off',
          charge_type: 'special_assessment',
          default_debtor: 'owner',
          frequency: 'once',
          start: f.saStart,
          amount_mode: 'per_unit',
          allocation_key_id: f.keyId,
          scope: { type: 'site', id: null },
          amount: { amount: Number(f.saAmount), currency: ctx.currency },
        },
      }
  }
}

/**
 * The "apply" step of a decision (G5): offers to create the change the decision describes, shows
 * what would be created (dry run), and only then, after a confirmation, creates it. The apply call
 * keeps ONE Idempotency-Key for its whole life so a retry or the replay after re-authentication can
 * never create the record twice.
 */
export function ApplyDecisionDialog({
  ctx,
  decision,
  suggestedKind,
  onClose,
}: {
  ctx: SiteCtx
  decision: S['Decision']
  suggestedKind: Kind | null
  onClose: () => void
}) {
  const { t } = useLingui()
  const labels = useGovLabels()
  const persons = usePersons(ctx.orgId)
  const plans = useOrgQuery(['charge-plans', ctx.siteId], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/charge-plans', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items.filter((p) => p.charge_type === 'dues')),
  )
  const budgets = useOrgQuery(['budgets', ctx.siteId], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/budgets', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items.filter((b) => b.status === 'draft')),
  )
  const keys = useOrgQuery(['alloc-keys', ctx.siteId], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/allocation-keys', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items),
  )

  const today = todayIn(ctx.timeZone)
  const [kind, setKind] = useState<Kind>(suggestedKind ?? 'dues_rate')
  const [f, setF] = useState<FormState>({
    planId: '',
    from: today,
    amount: null,
    personId: '',
    role: 'site_manager',
    validFrom: today,
    validTo: addDays(today, 365),
    replacesMembershipIds: [],
    budgetId: '',
    saName: '',
    saAmount: null,
    saStart: today,
    keyId: '',
  })
  const set = (patch: Partial<FormState>) => setF((cur) => ({ ...cur, ...patch }))
  // Electing someone ends nobody by default; the people replaced are chosen from the current
  // holders of the same role at this site.
  const holders = useOrgQuery(['role-holders', ctx.siteId, f.role], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/memberships', {
        params: {
          path: { org_id: ctx.orgId },
          query: {
            role: f.role,
            scope_type: 'site',
            scope_id: ctx.siteId,
            active: true,
            limit: 100,
          },
        },
      }),
    ).then((r) => r.items),
  )
  const [step, setStep] = useState<'form' | 'confirm'>('form')
  const [preview, setPreview] = useState<Record<string, unknown> | null>(null)
  const [error, setError] = useState<string | null>(null)
  const { currency, siteId } = ctx
  const body = useMemo(
    () => buildApplyBody(kind, f, { currency, siteId }),
    [kind, f, currency, siteId],
  )
  const applyErrorMessage = (e: unknown) => {
    if (e instanceof AppError && e.code === 'idempotency_mismatch')
      return t`The values changed after the last attempt. Review them and confirm again.`
    if (e instanceof AppError && e.code === 'locked_period')
      return t`The accounting period of this change is locked.`
    if (e instanceof AppError && e.status === 409)
      return t`This decision already has an applied change.`
    return t`Could not apply the decision. You can try again; nothing is created twice.`
  }

  // keys follow the body: an edit after a failed apply never reuses the old key with a new body
  const applyKey = useBodyIdempotencyKey(body)
  const previewKey = useBodyIdempotencyKey(body)

  const review = useOrgMutation(
    async () =>
      unwrap(
        gApi.POST('/v1/orgs/{org_id}/decisions/{decision_id}:apply', {
          params: {
            path: { org_id: ctx.orgId, decision_id: decision.id },
            query: { dry_run: true },
            header: idemHeader(previewKey.key),
          },
          body: body!,
        }),
      ),
    { invalidate: false },
  )
  const apply = useOrgMutation(async () =>
    run(() =>
      unwrap(
        gApi.POST('/v1/orgs/{org_id}/decisions/{decision_id}:apply', {
          params: {
            path: { org_id: ctx.orgId, decision_id: decision.id },
            header: idemHeader(applyKey.key),
          },
          body: body!,
        }),
      ),
    ),
  )

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            <Trans>Apply the decision</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>
              Decision {decision.year}/{decision.sequence_no} can create the change it describes.
              You confirm each one; the link to the decision is kept for the audit trail.
            </Trans>
          </DialogDescription>
        </DialogHeader>
        {step === 'form' ? (
          <div className="flex flex-col gap-4">
            <Field label={t`What should be created`} htmlFor="ap-kind">
              <Select id="ap-kind" value={kind} onChange={(e) => setKind(e.target.value as Kind)}>
                {KINDS.map((k) => (
                  <option key={k} value={k}>
                    {labels.effect(k)}
                  </option>
                ))}
              </Select>
            </Field>
            {kind === 'dues_rate' ? (
              <>
                <Field label={t`Dues plan`} htmlFor="ap-plan">
                  <Select
                    id="ap-plan"
                    value={f.planId}
                    onChange={(e) => set({ planId: e.target.value })}
                  >
                    <option value="">{t`Choose a plan`}</option>
                    {(plans.data ?? []).map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field label={t`Effective from`} htmlFor="ap-from">
                  <Input
                    id="ap-from"
                    type="date"
                    dir="ltr"
                    value={f.from}
                    onChange={(e) => set({ from: e.target.value })}
                  />
                </Field>
                <Field label={t`New amount per unit`} htmlFor="ap-amount">
                  <MoneyInput
                    id="ap-amount"
                    currency={ctx.currency}
                    value={f.amount}
                    onChange={(v) => set({ amount: v })}
                  />
                </Field>
              </>
            ) : null}
            {kind === 'membership' ? (
              <>
                <Field label={t`Elected person`} htmlFor="ap-person">
                  <Select
                    id="ap-person"
                    value={f.personId}
                    onChange={(e) => set({ personId: e.target.value })}
                  >
                    <option value="">{t`Choose a person`}</option>
                    {(persons.data ?? []).map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.display_name}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field label={t`Role`} htmlFor="ap-role">
                  <Select
                    id="ap-role"
                    value={f.role}
                    onChange={(e) => set({ role: e.target.value, replacesMembershipIds: [] })}
                  >
                    {ELECTED_ROLES.map((r) => (
                      <option key={r} value={r}>
                        {labels.role(r)}
                      </option>
                    ))}
                  </Select>
                </Field>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label={t`Term starts`} htmlFor="ap-vf">
                    <Input
                      id="ap-vf"
                      type="date"
                      dir="ltr"
                      value={f.validFrom}
                      onChange={(e) => set({ validFrom: e.target.value })}
                    />
                  </Field>
                  <Field label={t`Term ends`} htmlFor="ap-vt">
                    <Input
                      id="ap-vt"
                      type="date"
                      dir="ltr"
                      value={f.validTo}
                      onChange={(e) => set({ validTo: e.target.value })}
                    />
                  </Field>
                </div>
                {(holders.data ?? []).length ? (
                  <fieldset className="flex flex-col gap-2">
                    <legend className="text-sm font-medium">
                      <Trans>Current holders this election replaces</Trans>
                    </legend>
                    {(holders.data ?? []).map((h) => (
                      <LabeledCheckbox
                        key={h.id}
                        label={h.person_display_name}
                        checked={f.replacesMembershipIds.includes(h.id)}
                        onChange={(e) =>
                          set({
                            replacesMembershipIds: e.target.checked
                              ? [...f.replacesMembershipIds, h.id]
                              : f.replacesMembershipIds.filter((x) => x !== h.id),
                          })
                        }
                      />
                    ))}
                  </fieldset>
                ) : null}
              </>
            ) : null}
            {kind === 'budget_final' ? (
              <Field label={t`Budget`} htmlFor="ap-budget">
                <Select
                  id="ap-budget"
                  value={f.budgetId}
                  onChange={(e) => set({ budgetId: e.target.value })}
                >
                  <option value="">{t`Choose a budget`}</option>
                  {(budgets.data ?? []).map((b) => (
                    <option key={b.id} value={b.id}>
                      {b.fiscal_year_id}
                    </option>
                  ))}
                </Select>
              </Field>
            ) : null}
            {kind === 'special_assessment' ? (
              <>
                <Field label={t`Name of the plan`} htmlFor="ap-sa-name">
                  <Input
                    id="ap-sa-name"
                    value={f.saName}
                    onChange={(e) => set({ saName: e.target.value })}
                  />
                </Field>
                <Field label={t`Amount per unit`} htmlFor="ap-sa-amount">
                  <MoneyInput
                    id="ap-sa-amount"
                    currency={ctx.currency}
                    value={f.saAmount}
                    onChange={(v) => set({ saAmount: v })}
                  />
                </Field>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label={t`Due from`} htmlFor="ap-sa-start">
                    <Input
                      id="ap-sa-start"
                      type="date"
                      dir="ltr"
                      value={f.saStart}
                      onChange={(e) => set({ saStart: e.target.value })}
                    />
                  </Field>
                  <Field label={t`Split by`} htmlFor="ap-sa-key">
                    <Select
                      id="ap-sa-key"
                      value={f.keyId}
                      onChange={(e) => set({ keyId: e.target.value })}
                    >
                      <option value="">{t`Choose a key`}</option>
                      {(keys.data ?? []).map((k) => (
                        <option key={k.id} value={k.id}>
                          {k.name}
                        </option>
                      ))}
                    </Select>
                  </Field>
                </div>
              </>
            ) : null}
            <FormRootError message={error ?? undefined} />
            <DialogFooter>
              <Button variant="outline" onClick={onClose}>
                <Trans>Cancel</Trans>
              </Button>
              <Button
                disabled={!body || review.isPending}
                onClick={() => {
                  setError(null)
                  review.mutate(undefined, {
                    onSuccess: (r) => {
                      setPreview((r.preview as Record<string, unknown> | null) ?? {})
                      setStep('confirm')
                    },
                    onError: () => setError(t`Could not check this change. Review the values.`),
                  })
                }}
              >
                <Trans>Review</Trans>
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            <Callout tone="warning" role="status">
              <Trans>
                This will create a {labels.effect(kind)} linked to this decision. It can be
                corrected later but not silently undone.
              </Trans>
            </Callout>
            {preview && Object.keys(preview).length > 0 ? (
              <dl
                className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm"
                aria-label={t`What will be created`}
              >
                {Object.entries(preview)
                  .filter(([k]) => k !== 'kind')
                  .map(([k, v]) => (
                    <div key={k} className="contents">
                      <dt className="text-muted-foreground">{k.replaceAll('_', ' ')}</dt>
                      <dd dir="ltr" className="break-all">
                        {typeof v === 'object' ? JSON.stringify(v) : String(v)}
                      </dd>
                    </div>
                  ))}
              </dl>
            ) : null}
            <FormRootError message={error ?? undefined} />
            <DialogFooter>
              <Button variant="outline" onClick={() => setStep('form')}>
                <Trans>Back</Trans>
              </Button>
              <Button
                disabled={apply.isPending}
                onClick={() => {
                  setError(null)
                  apply.mutate(undefined, {
                    onSuccess: (r) => {
                      if (!r) return // re-authentication was dismissed
                      applyKey.renew()
                      toast.info(t`The change has been created.`)
                      onClose()
                    },
                    onError: (e) => setError(applyErrorMessage(e)),
                  })
                }}
              >
                <Trans>Confirm and apply</Trans>
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
