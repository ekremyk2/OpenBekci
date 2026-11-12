import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { api } from '@/api/client'
import { draftApi } from '@/api/draft'
import { AppError } from '@/api/errors'
import { meQueryOptions, useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { FormError, FormField, SubmitButton, useApplyApiError } from '@/components/forms/FormField'
import { Badge } from '@/components/ui/badge'
import { buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { AuthShell } from './AuthShell'
import { rememberJoin } from './finish'

/** Site QR path: phone or e-mail, one-time code, then claim a unit (the manager approves). */
export function JoinPage({ code }: { code: string }) {
  const { me, isLoading } = useSession()
  const info = useQuery({
    queryKey: ['join', code],
    retry: false,
    queryFn: async () => {
      const { data } = await draftApi.GET('/v1/join/{code}', { params: { path: { code } } })
      if (!data) throw new Error('Empty join info')
      return data
    },
  })
  if (info.error instanceof AppError && info.error.status === 404) {
    return (
      <AuthShell
        title={<Trans>This join code no longer works</Trans>}
        description={
          <Trans>It was changed or switched off. Ask your manager for the new QR code.</Trans>
        }
      >
        <Link to="/login" search={{}} className={buttonVariants({ variant: 'outline' })}>
          <Trans>Go to sign in</Trans>
        </Link>
      </AuthShell>
    )
  }
  return (
    <AuthShell
      title={<Trans>Join your building</Trans>}
      description={info.data ? `${info.data.org_name}, ${info.data.site_name}` : undefined}
    >
      {info.isPending || isLoading ? (
        <Skeleton className="h-11 w-full" role="status" aria-busy="true" />
      ) : info.isError ? (
        <ErrorState error={info.error} onRetry={() => void info.refetch()} />
      ) : me ? (
        <ClaimStep code={code} />
      ) : (
        <SignUpStep code={code} />
      )}
    </AuthShell>
  )
}

function SignUpStep({ code }: { code: string }) {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const applyError = useApplyApiError()
  const [sent, setSent] = useState<{ contact: string; phone: boolean } | null>(null)
  const schema = useMemo(
    () =>
      z.object({
        contact: z
          .string()
          .trim()
          .min(1, t`Enter your phone number or email`),
        name: z.string().trim(),
        otp: sent?.phone
          ? z
              .string()
              .trim()
              .min(1, t`Enter the code from the SMS`)
          : z.string(),
      }),
    [t, sent],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { contact: '', name: '', otp: '' },
  })
  const { errors, isSubmitting } = form.formState
  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={form.handleSubmit(async (v) => {
        try {
          if (sent?.phone) {
            await api.POST('/v1/auth/session', {
              body: { method: 'sms_otp', identifier: sent.contact, code: v.otp, client: 'web' },
            })
            // the session query now answers: the page switches to the claim step
            await queryClient.fetchQuery({ ...meQueryOptions, staleTime: 0 })
            return
          }
          const phone = !v.contact.includes('@')
          await draftApi.POST('/v1/join/{code}:sign-up', {
            params: { path: { code } },
            body: {
              ...(phone ? { phone: v.contact } : { email: v.contact }),
              ...(v.name ? { name: v.name } : {}),
              locale: i18n.locale === 'en' ? 'en' : 'tr',
              client: 'web',
            },
          })
          rememberJoin(code)
          setSent({ contact: v.contact, phone })
        } catch (e) {
          applyError(form, e)
        }
      })}
    >
      {sent && !sent.phone ? (
        <p role="status">
          <Trans>
            We sent a sign-in link to your email. Open it on this device to continue; it works once.
          </Trans>
        </p>
      ) : (
        <>
          <FormField id="join-contact" label={t`Phone or email`} error={errors.contact?.message}>
            <Input
              id="join-contact"
              autoComplete="username"
              readOnly={!!sent}
              aria-describedby="join-contact-error"
              aria-invalid={!!errors.contact}
              {...form.register('contact')}
            />
          </FormField>
          {sent ? (
            <FormField id="join-otp" label={t`Code from the SMS`} error={errors.otp?.message}>
              <Input
                id="join-otp"
                inputMode="numeric"
                autoComplete="one-time-code"
                aria-describedby="join-otp-error"
                aria-invalid={!!errors.otp}
                {...form.register('otp')}
              />
            </FormField>
          ) : (
            <FormField id="join-name" label={t`Your name (optional)`}>
              <Input id="join-name" autoComplete="name" {...form.register('name')} />
            </FormField>
          )}
          <FormError message={errors.root?.message} />
          <SubmitButton busy={isSubmitting}>
            {sent ? <Trans>Continue</Trans> : <Trans>Send me a code</Trans>}
          </SubmitButton>
        </>
      )}
    </form>
  )
}

type Role = 'owner' | 'tenant' | 'household'

function ClaimStep({ code }: { code: string }) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const [unitId, setUnitId] = useState('')
  const [role, setRole] = useState<Role>('owner')
  const [error, setError] = useState<string>()
  const units = useQuery({
    queryKey: ['join', code, 'units'],
    queryFn: async () => {
      const { data } = await draftApi.GET('/v1/join/{code}/units', {
        params: { path: { code }, query: { limit: 200 } },
      })
      return data?.items ?? []
    },
  })
  const claims = useQuery({
    queryKey: ['me', 'unit-claims'],
    queryFn: async () => {
      const { data } = await draftApi.GET('/v1/me/unit-claims', { params: { query: {} } })
      return data?.items ?? []
    },
  })
  const claim = useMutation({
    mutationFn: () =>
      draftApi.POST('/v1/join/{code}/claims', {
        params: { path: { code } },
        body: { unit_id: unitId, claimed_role: role },
        ...withIdempotencyKey(idem.key),
      }),
    onSuccess: async () => {
      idem.renew()
      setUnitId('')
      setError(undefined)
      await queryClient.invalidateQueries({ queryKey: ['me', 'unit-claims'] })
    },
    onError: (e) =>
      setError(
        e instanceof AppError && e.status === 409
          ? t`You already asked for this unit.`
          : t`Something went wrong. Please try again.`,
      ),
  })
  const statusLabel = {
    pending: t`Waiting for approval`,
    approved: t`Approved`,
    rejected: t`Rejected`,
  }
  return (
    <>
      {claims.data && claims.data.length > 0 ? (
        <section aria-labelledby="my-claims" className="flex flex-col gap-2">
          <h2 id="my-claims" className="font-medium">
            <Trans>Your requests</Trans>
          </h2>
          <ul className="flex flex-col gap-2">
            {claims.data.map((c) => (
              <li key={c.id} className="flex items-center justify-between gap-2">
                <span>{c.unit_door_number}</span>
                <Badge variant={c.status === 'approved' ? 'success' : 'outline'}>
                  {statusLabel[c.status as keyof typeof statusLabel] ?? c.status}
                </Badge>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      <form
        noValidate
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          if (!unitId) return setError(t`Choose your unit`)
          claim.mutate()
        }}
      >
        <FormField id="claim-unit" label={t`Your unit`}>
          <Select
            id="claim-unit"
            value={unitId}
            disabled={units.isPending}
            onChange={(e) => setUnitId(e.target.value)}
          >
            <option value="">{t`Choose…`}</option>
            {units.data?.map((u) => (
              <option key={u.id} value={u.id}>
                {u.block_name} · {u.door_number}
              </option>
            ))}
          </Select>
        </FormField>
        <FormField id="claim-role" label={t`You are`}>
          <Select id="claim-role" value={role} onChange={(e) => setRole(e.target.value as Role)}>
            <option value="owner">{t`Owner`}</option>
            <option value="tenant">{t`Tenant`}</option>
            <option value="household">{t`Household member`}</option>
          </Select>
        </FormField>
        <FormError message={error} />
        <SubmitButton busy={claim.isPending}>
          <Trans>Ask to join this unit</Trans>
        </SubmitButton>
        <p className="text-muted-foreground">
          <Trans>Your manager approves the request. You will see its status here.</Trans>
        </p>
      </form>
    </>
  )
}
