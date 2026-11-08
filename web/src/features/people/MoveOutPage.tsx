import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { FileTextIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { todayInZone } from '@/lib/dates'
import { useActiveSite } from '@/features/sites/useActiveSite'
import { useSession } from '@/app/session'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { Money } from '@/components/data/Money'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { PersonPicker, type PickerOption } from '@/components/forms/EntityPicker'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { PageHeader } from '@/components/layout/PageHeader'
import { buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { openChargesLabel } from './labels'
import { personQueries } from './queries'

const OPEN_CHARGES = ['carry_to_person', 'keep_on_unit', 'write_off'] as const

/**
 * Move-out or sale (S7): pick the date, see the balance the person leaves, choose what happens to
 * the debt (default: stays with the person), confirm. The server files the closing statement and
 * the debt certificate.
 */
export function MoveOutPage({ occupancyId }: { occupancyId: string }) {
  const { org } = useSession()
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const orgId = org?.id ?? ''
  const occQ = useQuery({ ...personQueries.occupancy(orgId, occupancyId), enabled: !!org })
  const { timezone } = useActiveSite()
  const [asOf, setAsOf] = useState(() => todayInZone(timezone))
  const [transferTo, setTransferTo] = useState<PickerOption | null>(null)
  const [done, setDone] = useState<Schema<'MoveOut'> | null>(null)

  const schema = useMemo(
    () =>
      z.object({
        open_charges: z.string(),
        disposition_reason: z.string().trim(),
      }),
    [],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { open_charges: 'carry_to_person', disposition_reason: '' },
  })
  const choice = useWatch({ control: form.control, name: 'open_charges' })
  const previewQ = useQuery({
    queryKey: ['people.previewMoveOut', orgId, occupancyId, asOf, choice],
    enabled: !!org && /^\d{4}-\d{2}-\d{2}$/.test(asOf),
    queryFn: async () => {
      const { data } = await draftApi.POST(
        '/v1/orgs/{org_id}/occupancies/{occupancy_id}/move-out:preview',
        {
          params: { path: { org_id: orgId, occupancy_id: occupancyId } },
          body: { as_of: asOf, open_charges: choice as 'carry_to_person' },
          ...withIdempotencyKey(crypto.randomUUID()),
        },
      )
      return data!
    },
  })
  const preview = previewQ.data
  const owes = !!preview && preview.total.amount > 0

  const changed = !!preview && choice !== preview.default_open_charges
  const [formError, setFormError] = useState<Record<string, string>>({})

  const submit = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(async () => {
        const { data } = await draftApi.POST(
          '/v1/orgs/{org_id}/occupancies/{occupancy_id}/move-out',
          {
            params: {
              path: { org_id: orgId, occupancy_id: occupancyId },
              header: { 'Idempotency-Key': idem.key },
            },
            body: {
              as_of: asOf,
              open_charges: v.open_charges as 'carry_to_person',
              disposition_reason: v.disposition_reason || undefined,
              transferred_to_person_id:
                v.open_charges === 'carry_to_person' ? transferTo?.id : undefined,
            },
          },
        )
        return data!
      }),
    onSuccess: async (mo) => {
      idem.renew()
      setDone(mo)
      await queryClient.invalidateQueries({ queryKey: ['people.listOccupancies', orgId] })
      await queryClient.invalidateQueries({ queryKey: ['tenancy.listUnits', orgId] })
    },
    onError: (e) =>
      applyError(form, e, {
        open_charges: 'open_charges',
        transferred_to_person_id: 'open_charges',
      }),
  })

  if (occQ.isLoading) return <PageSkeleton />
  if (occQ.error || !occQ.data)
    return <ErrorState error={occQ.error} onRetry={() => void occQ.refetch()} />
  const occ = occQ.data
  const fileHref = (id: string) => `/v1/orgs/${orgId}/files/${id}`
  const { errors } = form.formState

  if (done) {
    return (
      <div className="flex flex-col gap-4">
        <PageHeader title={<Trans>{occ.person_display_name} moved out</Trans>} />
        <Card className="max-w-xl">
          <CardHeader>
            <CardTitle className="text-base">
              <Trans>Documents</Trans>
            </CardTitle>
          </CardHeader>
          <CardContent className="grid gap-3 text-sm" role="status">
            <p>
              <Trans>The move-out is recorded. The documents were also filed with the unit.</Trans>
            </p>
            {done.carried.length > 0 ? <CarriedList items={done.carried} /> : null}
            {done.closing_statement_file_id ? (
              <a
                className={buttonVariants({ variant: 'outline' })}
                href={fileHref(done.closing_statement_file_id)}
                download
              >
                <FileTextIcon aria-hidden="true" />
                <Trans>Closing statement</Trans>
              </a>
            ) : null}
            {done.certificate_file_id ? (
              <a
                className={buttonVariants({ variant: 'outline' })}
                href={fileHref(done.certificate_file_id)}
                download
              >
                <FileTextIcon aria-hidden="true" />
                <Trans>Debt status certificate</Trans>
              </a>
            ) : null}
            <Link
              to="/orgs/$orgId/people/$personId"
              params={{ orgId, personId: occ.person_id }}
              search={{ tab: 'units' }}
              className="underline-offset-4 hover:underline"
            >
              <Trans>Back to {occ.person_display_name}</Trans>
            </Link>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={<Trans>Move out or sale</Trans>}
        description={
          <Trans>
            {occ.person_display_name} leaves {occ.unit_door_number}. The whole month&apos;s dues
            stay with whoever is in the unit on the due date; nothing is pro-rated.
          </Trans>
        }
        crumbs={[
          { label: t`People`, to: '/orgs/$orgId/people' },
          {
            label: occ.person_display_name,
            to: '/orgs/$orgId/people/$personId',
            params: { personId: occ.person_id },
          },
          { label: t`Move out` },
        ]}
      />
      <form
        noValidate
        className="grid max-w-2xl gap-5"
        onSubmit={form.handleSubmit((v) => {
          const next: Record<string, string> = {}
          if (changed && owes && !v.disposition_reason)
            next.reason = t`Say why the debt is handled differently`
          setFormError(next)
          if (Object.keys(next).length) return
          return submit.mutateAsync(v).catch(() => undefined)
        })}
      >
        <Field id="mo-date" label={<Trans>Moves out on</Trans>}>
          <Input
            id="mo-date"
            type="date"
            value={asOf}
            onChange={(e) => setAsOf(e.target.value)}
            className="w-auto"
          />
        </Field>

        <section className="grid gap-2" aria-labelledby="mo-bal">
          <h2 id="mo-bal" className="text-base font-semibold">
            <Trans>Balance when they leave</Trans>
          </h2>
          {previewQ.isLoading ? <PageSkeleton /> : null}
          {previewQ.error ? (
            <ErrorState error={previewQ.error} onRetry={() => void previewQ.refetch()} />
          ) : null}
          {preview ? (
            <dl className="grid grid-cols-2 gap-2 rounded-md border p-4 text-sm sm:max-w-sm">
              <dt>
                <Trans>Dues and charges</Trans>
              </dt>
              <dd className="text-end">
                <Money minor={preview.principal.amount} currency={preview.principal.currency} />
              </dd>
              <dt>
                <Trans>Late fees</Trans>
              </dt>
              <dd className="text-end">
                <Money minor={preview.late_fee.amount} currency={preview.late_fee.currency} />
              </dd>
              <dt>
                <Trans>Credit</Trans>
              </dt>
              <dd className="text-end">
                <Money minor={preview.credit.amount} currency={preview.credit.currency} />
              </dd>
              <dt className="font-semibold">
                <Trans>Total</Trans>
              </dt>
              <dd className="text-end font-semibold">
                <Money minor={preview.total.amount} currency={preview.total.currency} />
              </dd>
            </dl>
          ) : null}
          {preview && preview.carried.length > 0 ? <CarriedList items={preview.carried} /> : null}
          <p className="text-xs text-muted-foreground">
            <Trans>
              When you confirm, a closing statement for the leaving person and a debt status
              certificate are created and filed with the unit.
            </Trans>
          </p>
        </section>

        <fieldset className="grid gap-2">
          <legend className="text-base font-semibold">
            <Trans>What happens to open charges</Trans>
          </legend>
          {OPEN_CHARGES.map((d) => (
            <label key={d} className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                value={d}
                className="accent-primary"
                {...form.register('open_charges')}
              />
              {openChargesLabel(d, i18n)}
              {preview?.default_open_charges === d ? (
                <span className="text-xs text-muted-foreground">
                  (<Trans>default</Trans>)
                </span>
              ) : null}
            </label>
          ))}
          {choice === 'carry_to_person' ? (
            <Field
              id="mo-to"
              label={<Trans>Carry it to another person instead (optional)</Trans>}
              error={formError.person}
            >
              <PersonPicker
                id="mo-to"
                orgId={orgId}
                value={transferTo}
                onChange={setTransferTo}
                invalid={!!formError.person}
              />
            </Field>
          ) : null}
          {changed ? (
            <Field
              id="mo-reason"
              label={<Trans>Reason (audited)</Trans>}
              error={formError.reason ?? errors.disposition_reason?.message}
            >
              <Input
                id="mo-reason"
                aria-invalid={!!formError.reason}
                {...form.register('disposition_reason')}
              />
            </Field>
          ) : null}
        </fieldset>

        <FormError message={errors.root?.message ?? errors.open_charges?.message} />
        <div>
          <SubmitButton busy={submit.isPending} disabled={!preview}>
            <Trans>Confirm move-out</Trans>
          </SubmitButton>
        </div>
      </form>
    </div>
  )
}

/** Open charges and what happens to each of them (only the ones the move-out touches). */
function CarriedList({ items }: { items: Schema<'CarriedCharge'>[] }) {
  const { t } = useLingui()
  return (
    <ul aria-label={t`Open charges`} className="divide-y rounded-md border text-sm sm:max-w-md">
      {items.map((c) => (
        <li key={c.charge_id} className="flex items-center justify-between gap-2 p-2">
          <span>
            {c.charge_type}
            {c.period ? ` ${c.period}` : ''}
          </span>
          <Money minor={c.balance.amount} currency={c.balance.currency} />
          <span className="text-xs text-muted-foreground">
            {c.outcome === 'carried_to_person'
              ? t`carried to the person`
              : c.outcome === 'kept_on_unit'
                ? t`kept on the unit`
                : t`written off`}
          </span>
        </li>
      ))}
    </ul>
  )
}
