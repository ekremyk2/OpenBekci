import { useMemo } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { draftApi } from '@/api/draft'
import { ErrorState } from '@/components/data/ErrorState'
import { FormError, FormField, SubmitButton, useApplyApiError } from '@/components/forms/FormField'
import { Skeleton } from '@/components/ui/skeleton'
import { buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { AuthShell } from './AuthShell'
import { landAfterSignIn } from './finish'

/** Accept an invitation: preview it, set a password (new account) and land signed in. */
export function InvitePage({ token }: { token: string }) {
  const { t } = useLingui()
  const preview = useQuery({
    queryKey: ['invitation', token],
    retry: false,
    queryFn: async () => {
      const { data } = await draftApi.GET('/v1/invitations/{token}', {
        params: { path: { token } },
      })
      if (!data) throw new Error('Empty invitation preview')
      return data
    },
  })
  const inv = preview.data
  if (preview.error instanceof AppError && preview.error.status === 404) {
    return (
      <AuthShell
        title={<Trans>This invitation no longer works</Trans>}
        description={<Trans>It was already used or has expired. Ask for a new one.</Trans>}
      >
        <Link to="/login" search={{}} className={buttonVariants()}>
          <Trans>Go to sign in</Trans>
        </Link>
      </AuthShell>
    )
  }
  return (
    <AuthShell
      title={<Trans>Accept your invitation</Trans>}
      description={
        inv
          ? [inv.org_name, inv.site_name, inv.unit_door_number].filter(Boolean).join(' · ')
          : undefined
      }
    >
      {preview.isPending ? (
        <div role="status" aria-busy="true" aria-label={t`Loading`} className="flex flex-col gap-3">
          <Skeleton className="h-11 w-full" />
          <Skeleton className="h-11 w-full" />
        </div>
      ) : inv ? (
        <AcceptForm token={token} existing={inv.existing_account} name={inv.person_display_name} />
      ) : (
        <ErrorState error={preview.error} onRetry={() => void preview.refetch()} />
      )}
    </AuthShell>
  )
}

function AcceptForm({
  token,
  existing,
  name,
}: {
  token: string
  existing: boolean
  name: string | null
}) {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const applyError = useApplyApiError()
  const schema = useMemo(
    () =>
      z.object({
        display_name: z.string().trim(),
        password: existing ? z.string() : z.string().min(10, t`Use at least 10 characters`),
      }),
    [t, existing],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { display_name: name ?? '', password: '' },
  })
  const { errors, isSubmitting } = form.formState
  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={form.handleSubmit(async (v) => {
        try {
          await draftApi.POST('/v1/invitations/{token}:accept', {
            params: { path: { token } },
            body: {
              ...(existing ? {} : { password: v.password }),
              ...(v.display_name ? { display_name: v.display_name } : {}),
              locale: i18n.locale,
              client: 'web',
            },
          })
          await navigate({ ...(await landAfterSignIn(queryClient)), replace: true })
        } catch (e) {
          applyError(form, e)
        }
      })}
    >
      {existing ? (
        <p>
          <Trans>You already have an account. Accepting links this invitation to it.</Trans>
        </p>
      ) : null}
      <FormField id="inv-name" label={t`Your name`} error={errors.display_name?.message}>
        <Input
          id="inv-name"
          autoComplete="name"
          aria-describedby="inv-name-error"
          {...form.register('display_name')}
        />
      </FormField>
      {existing ? null : (
        <FormField
          id="inv-password"
          label={t`Choose a password`}
          hint={t`At least 10 characters.`}
          error={errors.password?.message}
        >
          <Input
            id="inv-password"
            type="password"
            autoComplete="new-password"
            aria-describedby="inv-password-hint inv-password-error"
            aria-invalid={!!errors.password}
            {...form.register('password')}
          />
        </FormField>
      )}
      <FormError message={errors.root?.message} />
      <SubmitButton busy={isSubmitting}>
        <Trans>Accept and sign in</Trans>
      </SubmitButton>
    </form>
  )
}
