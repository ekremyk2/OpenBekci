import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { EyeIcon, EyeOffIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { isValidNationalId } from './tckn'

/** The revealed national id hides itself again after this long. */
const REVEAL_MS = 30_000

/**
 * National id (S6): masked by default; "Show" asks for re-authentication, then reads the number.
 * Entry is checksum-validated (TCKN for TR) before it is sent.
 */
export function NationalIdPanel({ orgId, person }: { orgId: string; person: Schema<'Person'> }) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  // The revealed number lives only in this component's state: never in a query or mutation cache
  // (those keep results after unmount), cleared on unmount and hidden again after 30 seconds.
  const [shown, setShown] = useState<string | null>(null)
  const [revealing, setRevealing] = useState(false)
  useEffect(() => {
    if (shown === null) return
    const timer = setTimeout(() => setShown(null), REVEAL_MS)
    return () => clearTimeout(timer)
  }, [shown])
  useEffect(() => () => setShown(null), [])
  const [editing, setEditing] = useState(false)
  const schema = useMemo(
    () =>
      z
        .object({
          country_code: z.string().length(2),
          value: z
            .string()
            .trim()
            .min(1, t`Enter the number`),
        })
        .refine((v) => isValidNationalId(v.country_code, v.value), {
          path: ['value'],
          message: t`This number is not valid. Check the digits.`,
        }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { country_code: person.national_id_country ?? 'TR', value: '' },
  })
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['people.getPerson', orgId, person.id] })

  async function reveal() {
    setRevealing(true)
    try {
      const d = await runWithReauth(async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/persons/{person_id}/national-id', {
          params: { path: { org_id: orgId, person_id: person.id } },
        })
        return data as (Schema<'NationalId'> & { value?: string }) | undefined
      })
      setShown(d?.value ?? d?.masked ?? '')
    } catch {
      // the global error handler already reported it (or the user cancelled the re-authentication)
    } finally {
      setRevealing(false)
    }
  }
  const save = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(() =>
        draftApi.PUT('/v1/orgs/{org_id}/persons/{person_id}/national-id', {
          params: { path: { org_id: orgId, person_id: person.id } },
          body: { country_code: v.country_code, value: v.value.replace(/\s+/g, '') },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      idem.renew()
      form.reset({ country_code: form.getValues('country_code'), value: '' })
      setEditing(false)
      setShown(null)
      await invalidate()
    },
    onError: (e) => applyError(form, e),
  })
  const clear = useMutation({
    mutationFn: () =>
      runWithReauth((key) =>
        draftApi.DELETE('/v1/orgs/{org_id}/persons/{person_id}/national-id', {
          params: { path: { org_id: orgId, person_id: person.id } },
          ...withIdempotencyKey(key),
        }),
      ),
    onSuccess: async () => {
      setShown(null)
      await invalidate()
    },
  })
  const { errors } = form.formState

  return (
    <section className="grid max-w-2xl gap-3" aria-labelledby="nid-h">
      <h2 id="nid-h" className="text-base font-semibold">
        <Trans>National id</Trans>
      </h2>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Optional. Stored encrypted and shown to managers only, for legal follow-up and formal
          notices.
        </Trans>
      </p>
      {person.national_id_masked && !editing ? (
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-sm" dir="ltr" data-testid="national-id">
            {person.national_id_country} {shown ?? person.national_id_masked}
          </span>
          {shown === null ? (
            <Button variant="outline" size="sm" onClick={() => void reveal()} disabled={revealing}>
              <EyeIcon aria-hidden="true" />
              <Trans>Show</Trans>
            </Button>
          ) : (
            <Button variant="outline" size="sm" onClick={() => setShown(null)}>
              <EyeOffIcon aria-hidden="true" />
              <Trans>Hide</Trans>
            </Button>
          )}
          <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
            <Trans>Change</Trans>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => clear.mutate()}
            disabled={clear.isPending}
          >
            <Trans>Remove</Trans>
          </Button>
        </div>
      ) : editing || !person.national_id_masked ? (
        <form
          noValidate
          className="flex flex-wrap items-start gap-2"
          onSubmit={form.handleSubmit((v) => save.mutateAsync(v).catch(() => undefined))}
        >
          <Field id="nid-country" label={<Trans>Country</Trans>}>
            <Select id="nid-country" className="w-24" {...form.register('country_code')}>
              <option value="TR">TR</option>
              <option value="DE">DE</option>
              <option value="GB">GB</option>
            </Select>
          </Field>
          <Field id="nid-value" label={<Trans>Number</Trans>} error={errors.value?.message}>
            <Input
              id="nid-value"
              dir="ltr"
              inputMode="numeric"
              autoComplete="off"
              aria-invalid={!!errors.value}
              {...form.register('value')}
            />
          </Field>
          <div className="flex gap-2 pt-8">
            <SubmitButton busy={save.isPending}>
              <Trans>Save number</Trans>
            </SubmitButton>
            {editing ? (
              <Button type="button" variant="outline" onClick={() => setEditing(false)}>
                <Trans>Cancel</Trans>
              </Button>
            ) : null}
          </div>
          <div className="basis-full">
            <FormError message={errors.root?.message} />
          </div>
        </form>
      ) : null}
    </section>
  )
}
