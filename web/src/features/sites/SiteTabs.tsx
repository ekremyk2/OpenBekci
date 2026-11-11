import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, ifMatchOf, type Schema } from '@/api/draft'
import { AppError } from '@/api/errors'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { SiteDate } from '@/components/data/SiteDate'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import {
  PackDefaultBadge,
  packDefaultEntries,
  PackValue,
  useSettingLabel,
} from '@/features/packs/PackDefaultBadge'
import { SiteMerchantCard } from '@/features/settings/MerchantOfRecord'
import { siteQueries } from './queries'

/* ───────────────────────────── settings ───────────────────────────── */

export function SiteSettingsTab({
  orgId,
  siteId,
  canEdit,
}: {
  orgId: string
  siteId: string
  canEdit: boolean
}) {
  const query = useQuery(siteQueries.settings(orgId, siteId))
  if (query.isLoading) return <PageSkeleton />
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  return (
    <div className="flex flex-col gap-8">
      <SettingsForm orgId={orgId} siteId={siteId} settings={query.data!} canEdit={canEdit} />
      <SiteMerchantCard
        key={query.data!.version}
        orgId={orgId}
        siteId={siteId}
        settings={query.data!}
      />
    </div>
  )
}

const settingsSchema = z.object({
  tenants_are_portal_users: z.boolean(),
  directory_enabled: z.boolean(),
  owner_financial_transparency: z.boolean(),
  parking_storage_default: z.string(),
})

function SettingsForm({
  orgId,
  siteId,
  settings,
  canEdit,
}: {
  orgId: string
  siteId: string
  settings: Schema<'SiteSettings'>
  canEdit: boolean
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const applyError = useApplyFormError()
  const form = useForm({
    resolver: zodResolver(settingsSchema),
    defaultValues: {
      tenants_are_portal_users: settings.tenants_are_portal_users,
      directory_enabled: settings.directory_enabled,
      owner_financial_transparency: settings.owner_financial_transparency,
      parking_storage_default: settings.parking_storage_default,
    },
  })
  const [saved, setSaved] = useState(false)
  const save = useMutation({
    mutationFn: (v: z.infer<typeof settingsSchema>) =>
      runWithReauth(() =>
        draftApi.PATCH('/v1/orgs/{org_id}/sites/{site_id}/settings', {
          params: {
            path: { org_id: orgId, site_id: siteId },
            header: ifMatchOf(settings.version),
          },
          body: v as Schema<'SiteSettingsUpdate'>,
        }),
      ),
    onSuccess: async () => {
      setSaved(true)
      // settings and bank details carry the site's version: every save moves it for both
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['tenancy.getSiteSettings', orgId, siteId] }),
        queryClient.invalidateQueries({ queryKey: ['tenancy.getBankDetails', orgId, siteId] }),
        queryClient.invalidateQueries({ queryKey: ['tenancy.getSite', orgId] }),
      ])
    },
    onError: (e) => applyError(form, e),
  })
  const { errors } = form.formState
  const toggle = (
    name: 'tenants_are_portal_users' | 'directory_enabled' | 'owner_financial_transparency',
    label: React.ReactNode,
    hint: React.ReactNode,
  ) => (
    <label className="flex items-start gap-3">
      <Checkbox disabled={!canEdit} className="mt-1" {...form.register(name)} />
      <span className="flex flex-col">
        <span className="text-sm font-medium">{label}</span>
        <span className="text-xs text-muted-foreground">{hint}</span>
      </span>
    </label>
  )
  return (
    <form
      noValidate
      className="grid max-w-xl gap-5"
      onSubmit={form.handleSubmit((v) => {
        setSaved(false)
        return save.mutateAsync(v).catch(() => undefined)
      })}
    >
      {toggle(
        'tenants_are_portal_users',
        <Trans>Tenants get their own login</Trans>,
        <Trans>The person on the lease sees the tenant side of the unit in the portal.</Trans>,
      )}
      {toggle(
        'directory_enabled',
        <Trans>Resident directory</Trans>,
        <Trans>
          Residents who opt in appear in a directory. Managers and the board always see everyone.
        </Trans>,
      )}
      {toggle(
        'owner_financial_transparency',
        <Trans>Show income and expenses to owners</Trans>,
        <Trans>
          Owners see the aggregate income and expense report and the budget. The debtor list stays
          private.
        </Trans>,
      )}
      <Field
        id="set-parking"
        label={<Trans>Parking and storage default</Trans>}
        error={errors.parking_storage_default?.message}
      >
        <Select id="set-parking" disabled={!canEdit} {...form.register('parking_storage_default')}>
          <option value="appurtenance">{t`Belongs to a flat (no dues of its own)`}</option>
          <option value="independent">{t`Independent unit (own owner and charges)`}</option>
        </Select>
      </Field>
      <FormError message={errors.root?.message} />
      {canEdit ? (
        <div className="flex items-center gap-3">
          <SubmitButton busy={save.isPending}>
            <Trans>Save settings</Trans>
          </SubmitButton>
          {saved ? (
            <span role="status" className="text-sm text-muted-foreground">
              <Trans>Saved.</Trans>
            </span>
          ) : null}
        </div>
      ) : null}
      <PackDefaultsList settings={settings} canRaw={canEdit} />
    </form>
  )
}

/** Settings a pack's presets filled in that this site has not changed since (plan 04 §3.7). */
function PackDefaultsList({
  settings,
  canRaw,
}: {
  settings: Schema<'SiteSettings'>
  canRaw: boolean
}) {
  const label = useSettingLabel()
  const [raw, setRaw] = useState(false)
  const entries = packDefaultEntries(settings)
  if (entries.length === 0) return null
  return (
    <section
      aria-labelledby="pack-defaults-title"
      className="flex flex-col gap-2 rounded-md border p-3"
    >
      <h2 id="pack-defaults-title" className="text-sm font-semibold">
        <Trans>Values from a pack</Trans>
      </h2>
      <p className="text-xs text-muted-foreground">
        <Trans>
          A pack filled these in. Change one in its own settings and it becomes this site&apos;s
          own.
        </Trans>
      </p>
      <ul className="divide-y text-sm">
        {entries.map(([key, entry]) => (
          <li key={key} className="flex flex-col gap-1 py-2">
            <span className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{label(key)}</span>
              <PackDefaultBadge pack={entry.pack} />
            </span>
            <PackValue value={entry.value} raw={raw} />
          </li>
        ))}
      </ul>
      {canRaw ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="self-start"
          aria-pressed={raw}
          onClick={() => setRaw((r) => !r)}
        >
          <Trans>Raw</Trans>
        </Button>
      ) : null}
    </section>
  )
}

/* ───────────────────────────── bank details ───────────────────────────── */

export function maskIban(iban: string): string {
  const compact = iban.replace(/\s+/g, '')
  if (compact.length <= 8) return compact
  return `${compact.slice(0, 4)} •••• •••• •••• ${compact.slice(-4)}`
}

function groupIban(iban: string): string {
  return iban
    .replace(/\s+/g, '')
    .replace(/(.{4})/g, '$1 ')
    .trim()
}

export function SiteBankTab({
  orgId,
  siteId,
  canEdit,
}: {
  orgId: string
  siteId: string
  canEdit: boolean
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const applyError = useApplyFormError()
  const idem = useIdempotencyKey()
  const query = useQuery(siteQueries.bank(orgId, siteId))
  // With no bank details yet, the site's own version is the one the server compares against.
  const site = useQuery(siteQueries.one(orgId, siteId))
  const [reveal, setReveal] = useState(false)
  const [editing, setEditing] = useState(false)
  const schema = useMemo(
    () =>
      z.object({
        iban: z
          .string()
          .trim()
          .transform((s) => s.replace(/\s+/g, '').toUpperCase())
          .refine(
            (s) => /^[A-Z]{2}\d{2}[A-Z0-9]{11,30}$/.test(s),
            t`Enter a valid IBAN, for example TR33 0006 1005 1978 6457 8413 26`,
          ),
        holder: z
          .string()
          .trim()
          .min(1, t`Enter the account holder`),
        bank_name: z.string().trim(),
        reference_hint: z.string().trim(),
      }),
    [t],
  )
  const bank = query.data
  const form = useForm({
    resolver: zodResolver(schema),
    values: {
      iban: bank?.iban ?? '',
      holder: bank?.holder ?? '',
      bank_name: bank?.bank_name ?? '',
      reference_hint: bank?.reference_hint ?? '',
    },
  })
  const save = useMutation({
    mutationFn: (v: z.output<typeof schema>) =>
      runWithReauth(() =>
        draftApi.PUT('/v1/orgs/{org_id}/sites/{site_id}/bank-details', {
          params: {
            path: { org_id: orgId, site_id: siteId },
            header: ifMatchOf(bank?.version ?? site.data?.version),
          },
          body: {
            iban: v.iban,
            holder: v.holder,
            bank_name: v.bank_name || null,
            reference_hint: v.reference_hint || null,
          },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      idem.renew()
      setEditing(false)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['tenancy.getBankDetails', orgId, siteId] }),
        queryClient.invalidateQueries({ queryKey: ['tenancy.getSiteSettings', orgId, siteId] }),
        queryClient.invalidateQueries({ queryKey: ['tenancy.getSite', orgId] }),
      ])
    },
    onError: (e) => applyError(form, e),
  })
  const { errors } = form.formState

  if (query.isLoading) return <PageSkeleton />
  const missing = query.error instanceof AppError && query.error.status === 404
  if (query.error && !missing)
    return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const showForm = canEdit && (editing || missing)

  return (
    <div className="grid max-w-xl gap-4">
      <p className="text-sm text-muted-foreground">
        <Trans>Residents see these details when they pay by bank transfer.</Trans>
      </p>
      {bank && !showForm ? (
        <div className="grid gap-3 text-sm">
          <dl className="grid gap-3">
            <div>
              <dt className="text-muted-foreground">
                <Trans>IBAN</Trans>
              </dt>
              <dd className="flex items-center gap-2 font-mono" dir="ltr">
                <span data-testid="iban" className="iban">
                  {reveal ? groupIban(bank.iban) : maskIban(bank.iban)}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  aria-pressed={reveal}
                  onClick={() => setReveal((r) => !r)}
                >
                  {reveal ? <Trans>Hide</Trans> : <Trans>Show</Trans>}
                </Button>
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">
                <Trans>Account holder</Trans>
              </dt>
              <dd>{bank.holder}</dd>
            </div>
            {bank.bank_name ? (
              <div>
                <dt className="text-muted-foreground">
                  <Trans>Bank</Trans>
                </dt>
                <dd>{bank.bank_name}</dd>
              </div>
            ) : null}
            {bank.reference_hint ? (
              <div>
                <dt className="text-muted-foreground">
                  <Trans>Payers should write</Trans>
                </dt>
                <dd>{bank.reference_hint}</dd>
              </div>
            ) : null}
          </dl>
          {canEdit ? (
            <div>
              <Button variant="outline" onClick={() => setEditing(true)}>
                <Trans>Edit bank details</Trans>
              </Button>
            </div>
          ) : null}
        </div>
      ) : null}
      {!bank && !showForm ? (
        <EmptyState
          title={<Trans>No bank details yet</Trans>}
          description={<Trans>Ask a manager to add the account residents should pay into.</Trans>}
        />
      ) : null}
      {showForm ? (
        <form
          noValidate
          className="grid gap-4"
          onSubmit={form.handleSubmit((v) => save.mutateAsync(v).catch(() => undefined))}
        >
          <Field id="bank-iban" label={<Trans>IBAN</Trans>} error={errors.iban?.message}>
            <Input
              id="bank-iban"
              dir="ltr"
              autoComplete="off"
              aria-invalid={!!errors.iban}
              {...form.register('iban')}
            />
          </Field>
          <Field
            id="bank-holder"
            label={<Trans>Account holder</Trans>}
            error={errors.holder?.message}
          >
            <Input id="bank-holder" aria-invalid={!!errors.holder} {...form.register('holder')} />
          </Field>
          <Field id="bank-name" label={<Trans>Bank</Trans>} error={errors.bank_name?.message}>
            <Input id="bank-name" {...form.register('bank_name')} />
          </Field>
          <Field
            id="bank-hint"
            label={<Trans>What payers should write in the description</Trans>}
            error={errors.reference_hint?.message}
          >
            <Input id="bank-hint" {...form.register('reference_hint')} />
          </Field>
          <FormError message={errors.root?.message} />
          <div className="flex gap-2">
            <SubmitButton busy={save.isPending}>
              <Trans>Save bank details</Trans>
            </SubmitButton>
            {bank ? (
              <Button type="button" variant="outline" onClick={() => setEditing(false)}>
                <Trans>Cancel</Trans>
              </Button>
            ) : null}
          </div>
        </form>
      ) : null}
    </div>
  )
}

/* ───────────────────────────── lock date ───────────────────────────── */

export function SiteLockTab({
  orgId,
  siteId,
  timezone,
}: {
  orgId: string
  siteId: string
  timezone: string
}) {
  const { t } = useLingui()
  const { can } = useSession()
  const queryClient = useQueryClient()
  const applyError = useApplyFormError()
  const idem = useIdempotencyKey()
  const query = useQuery(siteQueries.lock(orgId, siteId))
  const canLock = can('money.lock', { siteId })
  const schema = useMemo(
    () => z.object({ lock_date: z.string().regex(/^(\d{4}-\d{2}-\d{2})?$/, t`Enter a date`) }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    values: { lock_date: query.data?.lock_date ?? '' },
  })
  const save = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      // moving the date back answers 403 reauth_required; runWithReauth confirms and replays
      runWithReauth(() =>
        draftApi.PUT('/v1/orgs/{org_id}/sites/{site_id}/lock-date', {
          params: {
            path: { org_id: orgId, site_id: siteId },
            header: ifMatchOf(query.data?.version),
          },
          body: { lock_date: v.lock_date || null },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      idem.renew()
      await queryClient.invalidateQueries({ queryKey: ['money.getLockDate', orgId, siteId] })
    },
    onError: (e) => applyError(form, e),
  })
  if (query.isLoading) return <PageSkeleton />
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const current = query.data?.lock_date
  return (
    <div className="grid max-w-xl gap-4">
      <p className="text-sm text-muted-foreground">
        <Trans>
          Nothing dated on or before the lock date can be posted or changed. Moving the date back
          asks you to confirm it is you.
        </Trans>
      </p>
      <p className="text-sm">
        <Trans>Current lock date:</Trans>{' '}
        {current ? (
          <SiteDate value={current} timeZone={timezone} className="font-medium" />
        ) : (
          <Badge variant="secondary">
            <Trans>Not set</Trans>
          </Badge>
        )}
      </p>
      {canLock ? (
        <form
          noValidate
          className="grid gap-3"
          onSubmit={form.handleSubmit((v) => save.mutateAsync(v).catch(() => undefined))}
        >
          <Field
            id="lock-date"
            label={<Trans>Lock everything up to and including</Trans>}
            error={form.formState.errors.lock_date?.message}
          >
            <Input id="lock-date" type="date" {...form.register('lock_date')} />
          </Field>
          <FormError message={form.formState.errors.root?.message} />
          <div>
            <SubmitButton busy={save.isPending}>
              <Trans>Set lock date</Trans>
            </SubmitButton>
          </div>
        </form>
      ) : null}
    </div>
  )
}

/* ───────────────────────────── blocks ───────────────────────────── */

export function SiteBlocksTab({
  orgId,
  siteId,
  canEdit,
}: {
  orgId: string
  siteId: string
  canEdit: boolean
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const applyError = useApplyFormError()
  const idem = useIdempotencyKey()
  const query = useQuery(siteQueries.blocks(orgId, siteId))
  const schema = useMemo(
    () =>
      z.object({
        name: z
          .string()
          .trim()
          .min(1, t`Enter a block name`),
      }),
    [t],
  )
  const form = useForm({ resolver: zodResolver(schema), defaultValues: { name: '' } })
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['tenancy.listBlocks', orgId, siteId] })
  const add = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/blocks', {
          params: { path: { org_id: orgId } },
          body: { site_id: siteId, name: v.name },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      idem.renew()
      form.reset()
      await invalidate()
    },
    onError: (e) => applyError(form, e),
  })
  const remove = useMutation({
    mutationFn: (id: string) =>
      draftApi.DELETE('/v1/orgs/{org_id}/blocks/{block_id}', {
        params: {
          path: { org_id: orgId, block_id: id },
          header: ifMatchOf(query.data?.items.find((b) => b.id === id)?.version),
        },
      }),
    onSuccess: invalidate,
  })
  if (query.isLoading) return <PageSkeleton />
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const blocks = query.data!.items
  const single = blocks.length === 1 && blocks[0]!.is_default
  return (
    <div className="grid max-w-xl gap-4">
      <p className="text-sm text-muted-foreground">
        {single ? (
          <Trans>
            This site is a single building, so its one block is hidden everywhere. Add a block to
            turn it into a multi-block site.
          </Trans>
        ) : (
          <Trans>Costs can be split among one block&apos;s units or across the whole site.</Trans>
        )}
      </p>
      {single ? null : (
        <ul className="divide-y rounded-md border">
          {blocks.map((b) => (
            <li key={b.id} className="flex items-center justify-between gap-2 p-3 text-sm">
              <span>{b.name}</span>
              {canEdit ? (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => remove.mutate(b.id)}
                  disabled={remove.isPending}
                >
                  <Trans>Delete</Trans>
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {remove.error ? (
        <FormError
          message={
            remove.error instanceof AppError && remove.error.status === 409
              ? t`A block that still has units cannot be deleted.`
              : t`Something went wrong. Please try again.`
          }
        />
      ) : null}
      {canEdit ? (
        <form
          noValidate
          className="flex items-start gap-2"
          onSubmit={form.handleSubmit((v) => add.mutateAsync(v).catch(() => undefined))}
        >
          <Field
            id="block-name"
            label={<Trans>New block</Trans>}
            error={form.formState.errors.name?.message}
            className="flex-1"
          >
            <Input
              id="block-name"
              aria-invalid={!!form.formState.errors.name}
              {...form.register('name')}
            />
          </Field>
          <div className="pt-8">
            <SubmitButton busy={add.isPending}>
              <Trans>Add block</Trans>
            </SubmitButton>
          </div>
        </form>
      ) : null}
      <FormError message={form.formState.errors.root?.message} />
    </div>
  )
}
