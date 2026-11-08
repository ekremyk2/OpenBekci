import { useMemo } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, ifMatchOf } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { SitePicker } from '@/components/forms/SitePicker'
import { PageHeader } from '@/components/layout/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { useActiveSite } from '@/features/sites/useActiveSite'
import { SetupNav } from '@/features/units/UnitsPage'
import { cn } from '@/lib/utils'
import { KeyValuesEditor } from './KeyValuesEditor'
import { keyQueries } from './keys'

export function AllocationKeysPage({ search }: { search: { site?: string; key?: string } }) {
  const { org, can } = useSession()
  const { t } = useLingui()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const applyError = useApplyFormError()
  const idem = useIdempotencyKey()
  const { siteId, sites, setSite } = useActiveSite(search.site)
  const orgId = org?.id ?? ''
  const canManage = can('sites.manage', { siteId })
  const keysQ = useQuery({ ...keyQueries.list(orgId, siteId ?? ''), enabled: !!org && !!siteId })
  const keys = keysQ.data?.items ?? []
  const selected = keys.find((k) => k.id === search.key) ?? keys[0]

  const schema = useMemo(
    () =>
      z.object({
        code: z
          .string()
          .trim()
          .regex(
            /^[a-z][a-z0-9_]{1,31}$/,
            t`Use lowercase letters, digits and underscores, for example heating`,
          ),
        name: z
          .string()
          .trim()
          .min(1, t`Enter a name`),
        kind: z.string(),
      }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { code: '', name: '', kind: 'table' },
  })
  const create = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/allocation-keys', {
          params: { path: { org_id: orgId } },
          body: { site_id: siteId!, code: v.code, name: v.name, kind: v.kind as 'table' | 'share' },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async ({ data }) => {
      idem.renew()
      form.reset()
      await queryClient.invalidateQueries({ queryKey: ['tenancy.listAllocationKeys', orgId] })
      if (data) void navigate({ to: '.', search: { site: siteId, key: data.id } as never })
    },
    onError: (e) => applyError(form, e),
  })
  const remove = useMutation({
    mutationFn: (id: string) =>
      draftApi.DELETE('/v1/orgs/{org_id}/allocation-keys/{key_id}', {
        params: {
          path: { org_id: orgId, key_id: id },
          header: ifMatchOf(keys.find((k) => k.id === id)?.version),
        },
      }),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ['tenancy.listAllocationKeys', orgId] }),
  })
  const { errors } = form.formState

  const kindLabel = (kind: string) =>
    kind === 'share'
      ? t`By share`
      : kind === 'equal'
        ? t`Equal`
        : kind === 'area'
          ? t`By area`
          : kind === 'table'
            ? t`Custom table`
            : kind

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={<Trans>Allocation keys</Trans>}
        description={
          <Trans>
            How a shared cost is split among units. Under the Condominium Law (Kat Mülkiyeti Kanunu,
            no. 634) the default is each flat&apos;s land share (arsa payı) unless your management
            plan says otherwise, so new sites start with that key. Add a custom table for things
            like heating meters.
          </Trans>
        }
      />
      <SetupNav />
      <SitePicker sites={sites} value={siteId} onChange={setSite} />
      {keysQ.isLoading ? <PageSkeleton /> : null}
      {keysQ.error ? <ErrorState error={keysQ.error} onRetry={() => void keysQ.refetch()} /> : null}
      {keysQ.data ? (
        <div className="grid gap-6 lg:grid-cols-[18rem_1fr]">
          <div className="flex flex-col gap-4">
            <ul aria-label={t`Allocation keys`} className="divide-y rounded-md border">
              {keys.map((k) => (
                <li
                  key={k.id}
                  className={cn(
                    'flex items-center justify-between gap-2 p-3',
                    selected?.id === k.id && 'bg-accent',
                  )}
                >
                  <button
                    className="flex flex-1 flex-col text-start outline-none focus-visible:ring-[3px] focus-visible:ring-ring"
                    aria-current={selected?.id === k.id ? 'true' : undefined}
                    onClick={() =>
                      void navigate({ to: '.', search: { site: siteId, key: k.id } as never })
                    }
                  >
                    <span className="text-sm font-medium">{k.name}</span>
                    <span className="text-xs text-muted-foreground">{k.code}</span>
                  </button>
                  <Badge variant="secondary">{kindLabel(k.kind)}</Badge>
                  {canManage && !k.is_system ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={t`Delete ${k.name}`}
                      onClick={() => remove.mutate(k.id)}
                    >
                      <Trans>Delete</Trans>
                    </Button>
                  ) : null}
                </li>
              ))}
            </ul>
            {canManage ? (
              <form
                noValidate
                className="grid gap-3 rounded-md border p-3"
                onSubmit={form.handleSubmit((v) => create.mutateAsync(v).catch(() => undefined))}
              >
                <h2 className="text-sm font-semibold">
                  <Trans>New custom key</Trans>
                </h2>
                <Field id="key-name" label={<Trans>Name</Trans>} error={errors.name?.message}>
                  <Input id="key-name" aria-invalid={!!errors.name} {...form.register('name')} />
                </Field>
                <Field id="key-code" label={<Trans>Code</Trans>} error={errors.code?.message}>
                  <Input
                    id="key-code"
                    dir="ltr"
                    aria-invalid={!!errors.code}
                    {...form.register('code')}
                  />
                </Field>
                <Field id="key-kind" label={<Trans>Kind</Trans>}>
                  <Select id="key-kind" {...form.register('kind')}>
                    <option value="table">{t`Custom table`}</option>
                    <option value="share">{t`By share`}</option>
                  </Select>
                </Field>
                <FormError message={errors.root?.message} />
                <div>
                  <SubmitButton busy={create.isPending}>
                    <Trans>Add key</Trans>
                  </SubmitButton>
                </div>
              </form>
            ) : null}
          </div>
          {selected && siteId ? (
            <section aria-labelledby="key-h" className="min-w-0">
              <h2 id="key-h" className="mb-3 text-lg font-semibold">
                {selected.name}
              </h2>
              <KeyValuesEditor
                key={selected.id}
                orgId={orgId}
                siteId={siteId}
                allocationKey={selected}
                canEdit={canManage}
              />
            </section>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
