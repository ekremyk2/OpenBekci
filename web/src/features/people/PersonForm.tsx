import { useMemo } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, ifMatch, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'

/** Create (no `person`) or edit a person: name, contacts, gate contact phone, notes. */
export function PersonForm({
  orgId,
  person,
  readOnly,
  onSaved,
}: {
  orgId: string
  person?: Schema<'Person'>
  readOnly?: boolean
  onSaved?: (person: Schema<'Person'>) => void
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const schema = useMemo(
    () =>
      z.object({
        display_name: z
          .string()
          .trim()
          .min(1, t`Enter the person's name`),
        email: z
          .string()
          .trim()
          .refine(
            (v) => v === '' || /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v),
            t`Enter a valid email address`,
          ),
        phone: z
          .string()
          .trim()
          .refine(
            (v) => v === '' || /^\+?[\d\s()-]{7,20}$/.test(v),
            t`Enter a valid phone number, for example +90 555 111 22 33`,
          ),
        preferred_channel: z.string(),
        gate_contact_phone: z
          .string()
          .trim()
          .refine((v) => v === '' || /^\+?[\d\s()-]{7,20}$/.test(v), t`Enter a valid phone number`),
        notes: z.string().trim(),
      }),
    [t],
  )
  const c = person?.contact
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: {
      display_name: person?.display_name ?? '',
      email: c?.email ?? '',
      phone: c?.phone ?? '',
      preferred_channel: c?.preferred_channel ?? '',
      gate_contact_phone: person?.gate_contact_phone ?? '',
      notes: person?.notes ?? '',
    },
  })
  const { errors } = form.formState
  const save = useMutation({
    mutationFn: (v: z.infer<typeof schema>) => {
      const body = {
        display_name: v.display_name,
        contact: {
          ...(person?.contact ?? {}),
          email: v.email || null,
          phone: v.phone || null,
          preferred_channel: (v.preferred_channel || null) as 'sms' | 'email' | null,
        },
        gate_contact_phone: v.gate_contact_phone || null,
        notes: v.notes || null,
      }
      return runWithReauth(async () => {
        if (person) {
          const { data } = await draftApi.PATCH('/v1/orgs/{org_id}/persons/{person_id}', {
            params: {
              path: { org_id: orgId, person_id: person.id },
              header: ifMatch(person.version),
            },
            body,
          })
          return data!
        }
        const { data } = await draftApi.POST('/v1/orgs/{org_id}/persons', {
          params: { path: { org_id: orgId } },
          body,
          ...withIdempotencyKey(idem.key),
        })
        return data!
      })
    },
    onSuccess: async (saved) => {
      idem.renew()
      await queryClient.invalidateQueries({ queryKey: ['people.listPersons', orgId] })
      await queryClient.invalidateQueries({ queryKey: ['people.getPerson', orgId] })
      onSaved?.(saved)
    },
    onError: (e) => applyError(form, e, { 'contact.email': 'email', 'contact.phone': 'phone' }),
  })
  return (
    <form
      noValidate
      className="grid max-w-2xl gap-4 sm:grid-cols-2"
      onSubmit={form.handleSubmit((v) => save.mutateAsync(v).catch(() => undefined))}
    >
      <Field
        id="person-name"
        label={<Trans>Full name</Trans>}
        error={errors.display_name?.message}
        className="sm:col-span-2"
      >
        <Input
          id="person-name"
          disabled={readOnly}
          aria-invalid={!!errors.display_name}
          {...form.register('display_name')}
        />
      </Field>
      <Field id="person-phone" label={<Trans>Phone</Trans>} error={errors.phone?.message}>
        <Input
          id="person-phone"
          type="tel"
          dir="ltr"
          disabled={readOnly}
          aria-invalid={!!errors.phone}
          {...form.register('phone')}
        />
      </Field>
      <Field id="person-email" label={<Trans>Email</Trans>} error={errors.email?.message}>
        <Input
          id="person-email"
          type="email"
          dir="ltr"
          disabled={readOnly}
          aria-invalid={!!errors.email}
          {...form.register('email')}
        />
      </Field>
      <Field id="person-channel" label={<Trans>Preferred channel</Trans>}>
        <Select id="person-channel" disabled={readOnly} {...form.register('preferred_channel')}>
          <option value="">{t`No preference`}</option>
          <option value="sms">SMS</option>
          <option value="email">{t`Email`}</option>
        </Select>
      </Field>
      <Field
        id="person-gate"
        label={<Trans>Gate contact phone</Trans>}
        error={errors.gate_contact_phone?.message}
        hint={
          <Trans>
            The number guards may call when a visitor arrives. Leave empty to use the phone above.
          </Trans>
        }
      >
        <Input
          id="person-gate"
          type="tel"
          dir="ltr"
          disabled={readOnly}
          aria-invalid={!!errors.gate_contact_phone}
          {...form.register('gate_contact_phone')}
        />
      </Field>
      <Field
        id="person-notes"
        label={<Trans>Notes (managers only)</Trans>}
        error={errors.notes?.message}
        className="sm:col-span-2"
      >
        <Textarea id="person-notes" disabled={readOnly} {...form.register('notes')} />
      </Field>
      <div className="flex flex-col gap-3 sm:col-span-2">
        <FormError message={errors.root?.message} />
        {readOnly ? null : (
          <div>
            <SubmitButton busy={save.isPending}>
              {person ? <Trans>Save changes</Trans> : <Trans>Add person</Trans>}
            </SubmitButton>
          </div>
        )}
      </div>
    </form>
  )
}
