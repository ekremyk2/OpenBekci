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

const CURRENCIES = ['TRY', 'EUR', 'USD', 'GBP']

function validZone(tz: string) {
  try {
    new Intl.DateTimeFormat('en', { timeZone: tz })
    return true
  } catch {
    return false
  }
}

function zones(): string[] {
  try {
    return Intl.supportedValuesOf('timeZone')
  } catch {
    return ['Europe/Istanbul']
  }
}

/** Create (no `site`) or edit a site: name, address, timezone, currency, fiscal year start. */
export function SiteForm({
  orgId,
  site,
  readOnly,
  onSaved,
}: {
  orgId: string
  site?: Schema<'Site'>
  readOnly?: boolean
  onSaved?: (site: Schema<'Site'>) => void
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const schema = useMemo(
    () =>
      z.object({
        name: z
          .string()
          .trim()
          .min(1, t`Enter the site name`),
        line1: z
          .string()
          .trim()
          .min(1, t`Enter the street address`),
        line2: z.string().trim(),
        district: z.string().trim(),
        city: z.string().trim(),
        postal_code: z.string().trim(),
        country_code: z
          .string()
          .trim()
          .length(2, t`Use a two-letter country code, for example TR`),
        timezone: z
          .string()
          .trim()
          .refine(validZone, t`Choose a valid time zone, for example Europe/Istanbul`),
        currency: z
          .string()
          .trim()
          .regex(/^[A-Za-z]{3}$/, t`Use a three-letter currency code, for example TRY`),
        fiscal_year_start_month: z.string(),
      }),
    [t],
  )
  const a = site?.address
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: {
      name: site?.name ?? '',
      line1: a?.line1 ?? '',
      line2: a?.line2 ?? '',
      district: a?.district ?? '',
      city: a?.city ?? '',
      postal_code: a?.postal_code ?? '',
      country_code: a?.country_code ?? 'TR',
      timezone: site?.timezone ?? 'Europe/Istanbul',
      currency: site?.currency ?? 'TRY',
      fiscal_year_start_month: String(site?.fiscal_year_start_month ?? 1),
    },
  })
  const { errors } = form.formState

  const save = useMutation({
    mutationFn: (v: z.infer<typeof schema>) => {
      const body = {
        name: v.name,
        address: {
          line1: v.line1,
          line2: v.line2 || null,
          district: v.district || null,
          city: v.city || null,
          postal_code: v.postal_code || null,
          country_code: v.country_code.toUpperCase(),
        },
        timezone: v.timezone,
        currency: v.currency.toUpperCase(),
        fiscal_year_start_month: Number(v.fiscal_year_start_month),
      }
      return runWithReauth(async () => {
        if (site) {
          const { data } = await draftApi.PATCH('/v1/orgs/{org_id}/sites/{site_id}', {
            params: { path: { org_id: orgId, site_id: site.id }, header: ifMatch(site.version) },
            body,
          })
          return data!
        }
        const { data } = await draftApi.POST('/v1/orgs/{org_id}/sites', {
          params: { path: { org_id: orgId } },
          body,
          ...withIdempotencyKey(idem.key),
        })
        return data!
      })
    },
    onSuccess: async (saved) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['tenancy.listSites', orgId] }),
        queryClient.invalidateQueries({ queryKey: ['tenancy.getSite', orgId] }),
        queryClient.invalidateQueries({ queryKey: ['me'] }), // the shell's site list
      ])
      idem.renew()
      onSaved?.(saved)
    },
    onError: (e) => applyError(form, e, { 'address.line1': 'line1', 'address.city': 'city' }),
  })

  const months = useMemo(
    () =>
      Array.from({ length: 12 }, (_, i) =>
        new Date(2000, i, 1).toLocaleString(undefined, { month: 'long' }),
      ),
    [],
  )

  return (
    <form
      noValidate
      className="grid max-w-2xl gap-4 sm:grid-cols-2"
      onSubmit={form.handleSubmit((v) => save.mutateAsync(v).catch(() => undefined))}
    >
      <Field
        id="site-name"
        label={<Trans>Site name</Trans>}
        error={errors.name?.message}
        className="sm:col-span-2"
      >
        <Input
          id="site-name"
          disabled={readOnly}
          aria-invalid={!!errors.name}
          {...form.register('name')}
        />
      </Field>
      <Field
        id="site-line1"
        label={<Trans>Street address</Trans>}
        error={errors.line1?.message}
        className="sm:col-span-2"
      >
        <Input
          id="site-line1"
          disabled={readOnly}
          autoComplete="address-line1"
          aria-invalid={!!errors.line1}
          {...form.register('line1')}
        />
      </Field>
      <Field
        id="site-line2"
        label={<Trans>Address line 2</Trans>}
        error={errors.line2?.message}
        className="sm:col-span-2"
      >
        <Input id="site-line2" disabled={readOnly} {...form.register('line2')} />
      </Field>
      <Field id="site-district" label={<Trans>District</Trans>} error={errors.district?.message}>
        <Input id="site-district" disabled={readOnly} {...form.register('district')} />
      </Field>
      <Field id="site-city" label={<Trans>City</Trans>} error={errors.city?.message}>
        <Input id="site-city" disabled={readOnly} {...form.register('city')} />
      </Field>
      <Field
        id="site-postal"
        label={<Trans>Postal code</Trans>}
        error={errors.postal_code?.message}
      >
        <Input
          id="site-postal"
          disabled={readOnly}
          inputMode="numeric"
          {...form.register('postal_code')}
        />
      </Field>
      <Field id="site-country" label={<Trans>Country</Trans>} error={errors.country_code?.message}>
        <Input
          id="site-country"
          disabled={readOnly}
          maxLength={2}
          aria-invalid={!!errors.country_code}
          {...form.register('country_code')}
        />
      </Field>
      <Field id="site-timezone" label={<Trans>Time zone</Trans>} error={errors.timezone?.message}>
        <Input
          id="site-timezone"
          list="site-timezones"
          disabled={readOnly}
          aria-invalid={!!errors.timezone}
          {...form.register('timezone')}
        />
        <datalist id="site-timezones">
          {zones().map((z) => (
            <option key={z} value={z} />
          ))}
        </datalist>
      </Field>
      <Field
        id="site-currency"
        label={<Trans>Currency</Trans>}
        error={errors.currency?.message}
        hint={<Trans>One currency per site.</Trans>}
      >
        <Input
          id="site-currency"
          list="site-currencies"
          readOnly={readOnly || !!site}
          maxLength={3}
          aria-invalid={!!errors.currency}
          {...form.register('currency')}
        />
        <datalist id="site-currencies">
          {CURRENCIES.map((c) => (
            <option key={c} value={c} />
          ))}
        </datalist>
      </Field>
      <Field
        id="site-fy"
        label={<Trans>Fiscal year starts in</Trans>}
        error={errors.fiscal_year_start_month?.message}
      >
        <Select id="site-fy" disabled={readOnly} {...form.register('fiscal_year_start_month')}>
          {months.map((m, i) => (
            <option key={m} value={String(i + 1)}>
              {m}
            </option>
          ))}
        </Select>
      </Field>
      <div className="flex flex-col gap-3 sm:col-span-2">
        <FormError message={errors.root?.message} />
        {readOnly ? null : (
          <div>
            <SubmitButton busy={save.isPending}>
              {site ? <Trans>Save changes</Trans> : <Trans>Create site</Trans>}
            </SubmitButton>
          </div>
        )}
      </div>
    </form>
  )
}
