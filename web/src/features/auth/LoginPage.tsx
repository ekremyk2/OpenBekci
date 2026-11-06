import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useForm, type FieldValues, type Path, type UseFormReturn } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { LoaderCircleIcon } from 'lucide-react'
import { AppError } from '@/api/errors'
import { api } from '@/api/client'
import type { SessionRequest } from '@/api/schema'
import { useInstance, supportedAuthMethods, type KnownAuthMethod } from '@/app/instance'
import { defaultLanding, meQueryOptions } from '@/app/session'
import { safeNext } from '@/app/guards'
import { ErrorState } from '@/components/data/ErrorState'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

/*
 * PROVISIONAL CONTRACT — `POST /v1/auth/session` is defined by a later backend task. These forms are
 * built against the shape in the web plan:
 *   { method: 'password' | 'sms_otp' | 'magic_link', identifier, password?, code?, client: 'web' }
 *   200            session cookie set (password, or sms_otp with a code)
 *   202            a one-time code / link was sent (sms_otp without code; magic_link)
 *   401 invalid_grant   wrong credentials or code      422 validation_failed + errors[].field
 *   429 rate_limited    handled globally (toast)
 * Adjust here (and only here) when the generated client lands.
 */

type SignInResult = 'signed-in' | 'code-sent'

function useSignIn() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return async (
    body: Omit<SessionRequest, 'client'>,
    next: string | undefined,
  ): Promise<SignInResult> => {
    const { response } = await api.POST('/v1/auth/session', { body: { ...body, client: 'web' } })
    if (response.status === 202) return 'code-sent'
    const me = await queryClient.fetchQuery({ ...meQueryOptions, staleTime: 0 })
    const target = safeNext(next)
    if (target) await navigate({ href: target, replace: true })
    else await navigate({ ...defaultLanding(me), replace: true })
    return 'signed-in'
  }
}

/** Maps an API failure onto the form: field errors where the server named them, else a form message. */
function useApplyError() {
  const { t } = useLingui()
  return <T extends FieldValues>(form: UseFormReturn<T>, error: unknown) => {
    if (error instanceof AppError) {
      if (error.isValidation && Object.keys(error.fieldErrors).length > 0) {
        for (const [field, issues] of Object.entries(error.fieldErrors)) {
          form.setError(field as Path<T>, {
            message: issues[0]?.message ?? t`This value is not valid`,
          })
        }
        return
      }
      if (error.status === 401) {
        form.setError('root', { message: t`That did not work. Check your details and try again.` })
        return
      }
      if (error.isRateLimited) {
        form.setError('root', { message: t`Too many attempts. Please wait and try again.` })
        return
      }
    }
    form.setError('root', { message: t`Something went wrong. Please try again.` })
  }
}

function Field({
  id,
  label,
  error,
  children,
}: {
  id: string
  label: string
  error?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  )
}

function FormError({ message }: { message?: string }) {
  return message ? (
    <p role="alert" className="text-sm text-destructive">
      {message}
    </p>
  ) : null
}

function SubmitButton({ busy, children }: { busy: boolean; children: React.ReactNode }) {
  return (
    <Button type="submit" disabled={busy}>
      {busy ? <LoaderCircleIcon className="animate-spin" aria-hidden="true" /> : null}
      {children}
    </Button>
  )
}

function PasswordForm({ next }: { next?: string }) {
  const { t } = useLingui()
  const signIn = useSignIn()
  const applyError = useApplyError()
  const schema = useMemo(
    () =>
      z.object({
        identifier: z
          .string()
          .trim()
          .min(1, t`Enter your email or phone number`),
        password: z.string().min(1, t`Enter your password`),
      }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { identifier: '', password: '' },
  })
  const { errors, isSubmitting } = form.formState
  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={form.handleSubmit(async (v) => {
        try {
          await signIn({ method: 'password', ...v }, next)
        } catch (e) {
          applyError(form, e)
        }
      })}
    >
      <Field id="pw-identifier" label={t`Email or phone`} error={errors.identifier?.message}>
        <Input
          id="pw-identifier"
          autoComplete="username"
          aria-describedby="pw-identifier-error"
          aria-invalid={!!errors.identifier}
          {...form.register('identifier')}
        />
      </Field>
      <Field id="pw-password" label={t`Password`} error={errors.password?.message}>
        <Input
          id="pw-password"
          type="password"
          autoComplete="current-password"
          aria-describedby="pw-password-error"
          aria-invalid={!!errors.password}
          {...form.register('password')}
        />
      </Field>
      <FormError message={errors.root?.message} />
      <SubmitButton busy={isSubmitting}>
        <Trans>Sign in</Trans>
      </SubmitButton>
    </form>
  )
}

function SmsForm({ next }: { next?: string }) {
  const { t } = useLingui()
  const signIn = useSignIn()
  const applyError = useApplyError()
  const [sentTo, setSentTo] = useState<string | null>(null)
  const schema = useMemo(
    () =>
      z.object({
        identifier: z
          .string()
          .trim()
          .min(1, t`Enter your phone number`),
        code: sentTo
          ? z
              .string()
              .trim()
              .min(1, t`Enter the code from the SMS`)
          : z.string().optional(),
      }),
    [t, sentTo],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { identifier: '', code: '' },
  })
  const { errors, isSubmitting } = form.formState
  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={form.handleSubmit(async (v) => {
        try {
          const result = await signIn(
            { method: 'sms_otp', identifier: v.identifier, ...(sentTo ? { code: v.code } : {}) },
            next,
          )
          if (result === 'code-sent') setSentTo(v.identifier)
        } catch (e) {
          applyError(form, e)
        }
      })}
    >
      <Field id="sms-identifier" label={t`Phone number`} error={errors.identifier?.message}>
        <Input
          id="sms-identifier"
          type="tel"
          autoComplete="tel"
          readOnly={!!sentTo}
          aria-describedby="sms-identifier-error"
          aria-invalid={!!errors.identifier}
          {...form.register('identifier')}
        />
      </Field>
      {sentTo ? (
        <Field id="sms-code" label={t`Code from the SMS`} error={errors.code?.message}>
          <Input
            id="sms-code"
            inputMode="numeric"
            autoComplete="one-time-code"
            aria-describedby="sms-code-error"
            aria-invalid={!!errors.code}
            {...form.register('code')}
          />
        </Field>
      ) : null}
      <FormError message={errors.root?.message} />
      <SubmitButton busy={isSubmitting}>
        {sentTo ? <Trans>Sign in</Trans> : <Trans>Send me a code</Trans>}
      </SubmitButton>
    </form>
  )
}

function MagicLinkForm() {
  const { t } = useLingui()
  const signIn = useSignIn()
  const applyError = useApplyError()
  const [sent, setSent] = useState(false)
  const schema = useMemo(
    () =>
      z.object({
        identifier: z
          .string()
          .trim()
          .min(1, t`Enter your email`),
      }),
    [t],
  )
  const form = useForm({ resolver: zodResolver(schema), defaultValues: { identifier: '' } })
  const { errors, isSubmitting } = form.formState
  if (sent) {
    return (
      <p role="status" className="text-sm">
        <Trans>If that address is registered, a sign-in link is on its way. It works once.</Trans>
      </p>
    )
  }
  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={form.handleSubmit(async (v) => {
        try {
          await signIn({ method: 'magic_link', identifier: v.identifier }, undefined)
          setSent(true)
        } catch (e) {
          applyError(form, e)
        }
      })}
    >
      <Field id="ml-identifier" label={t`Email`} error={errors.identifier?.message}>
        <Input
          id="ml-identifier"
          type="email"
          autoComplete="email"
          aria-describedby="ml-identifier-error"
          aria-invalid={!!errors.identifier}
          {...form.register('identifier')}
        />
      </Field>
      <FormError message={errors.root?.message} />
      <SubmitButton busy={isSubmitting}>
        <Trans>Email me a link</Trans>
      </SubmitButton>
    </form>
  )
}

function MethodLabel({ method }: { method: KnownAuthMethod }) {
  switch (method) {
    case 'password':
      return <Trans>Password</Trans>
    case 'sms_otp':
      return <Trans>SMS code</Trans>
    case 'magic_link':
      return <Trans>Email link</Trans>
  }
}

/** Only http(s) and same-origin paths are rendered as a logo. */
function safeLogoUrl(url: string | undefined): string | undefined {
  if (!url) return undefined
  return /^(https?:\/\/|\/(?![/\\]))/i.test(url) ? url : undefined
}

export function LoginPage({ next }: { next?: string }) {
  const instance = useInstance()
  const methods = supportedAuthMethods(instance.data)
  const first = methods[0]
  const logoUrl = safeLogoUrl(instance.data?.branding?.logo_url)

  return (
    <div className="flex min-h-dvh items-center justify-center p-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          {logoUrl ? <img src={logoUrl} alt="" className="mb-2 h-12 w-auto self-start" /> : null}
          <CardTitle className="text-xl">{instance.data?.instance_name || 'OpenBekci'}</CardTitle>
          <CardDescription>
            <Trans>Sign in to manage your building.</Trans>
          </CardDescription>
        </CardHeader>
        <CardContent>
          {instance.isPending ? (
            <div role="status" aria-busy="true" className="flex flex-col gap-3">
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-24" />
            </div>
          ) : instance.isError ? (
            <ErrorState error={instance.error} onRetry={() => void instance.refetch()} />
          ) : !first ? (
            <p role="alert" className="text-sm">
              <Trans>This server offers no sign-in method this app can use.</Trans>
            </p>
          ) : (
            <Tabs defaultValue={first}>
              {methods.length > 1 ? (
                <TabsList className="w-full">
                  {methods.map((m) => (
                    <TabsTrigger key={m} value={m}>
                      <MethodLabel method={m} />
                    </TabsTrigger>
                  ))}
                </TabsList>
              ) : null}
              {methods.includes('password') ? (
                <TabsContent value="password">
                  <PasswordForm next={next} />
                </TabsContent>
              ) : null}
              {methods.includes('sms_otp') ? (
                <TabsContent value="sms_otp">
                  <SmsForm next={next} />
                </TabsContent>
              ) : null}
              {methods.includes('magic_link') ? (
                <TabsContent value="magic_link">
                  <MagicLinkForm />
                </TabsContent>
              ) : null}
            </Tabs>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
