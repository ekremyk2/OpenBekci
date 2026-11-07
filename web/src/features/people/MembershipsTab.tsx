import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, ifMatch, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { roleLabel } from '@/lib/labels'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { SiteDate } from '@/components/data/SiteDate'
import { PageSkeleton } from '@/components/data/Skeletons'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { Badge } from '@/components/ui/badge'
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
import { Select } from '@/components/ui/select'
import { scopeLabel } from './labels'
import { personQueries } from './queries'

const ROLES = [
  'org_admin',
  'site_manager',
  'board_member',
  'auditor',
  'accountant',
  'staff',
  'guard',
] as const

/** Memberships (roles with a scope and a term). Removal runs through re-authentication. */
export function MembershipsTab({
  orgId,
  personId,
  canEdit,
}: {
  orgId: string
  personId: string
  canEdit: boolean
}) {
  const { t, i18n } = useLingui()
  const { org } = useSession()
  const queryClient = useQueryClient()
  const query = useQuery(personQueries.memberships(orgId, personId))
  const [removing, setRemoving] = useState<Schema<'Membership'> | null>(null)
  const sites = org?.sites ?? []
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['people.listMemberships', orgId] })
  const scopeName = (m: Schema<'Membership'>) =>
    m.scope_type === 'org'
      ? scopeLabel('org', i18n)
      : (sites.find((s) => s.id === m.scope_id)?.name ?? scopeLabel(m.scope_type, i18n))
  const tz = sites[0]?.timezone ?? 'UTC'

  const remove = useMutation({
    mutationFn: (m: Schema<'Membership'>) =>
      runWithReauth(() =>
        draftApi.DELETE('/v1/orgs/{org_id}/memberships/{membership_id}', {
          params: { path: { org_id: orgId, membership_id: m.id }, header: ifMatch(m.version) },
        }),
      ),
    onSuccess: async () => {
      setRemoving(null)
      await invalidate()
    },
  })

  if (query.isLoading) return <PageSkeleton />
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const items = query.data!.items
  return (
    <div className="grid gap-4">
      {items.length === 0 ? (
        <EmptyState
          title={<Trans>No roles</Trans>}
          description={<Trans>This person has no management or staff role.</Trans>}
        />
      ) : (
        <ul className="divide-y rounded-md border">
          {items.map((m) => (
            <li key={m.id} className="flex flex-wrap items-center gap-3 p-3 text-sm">
              <span className="font-medium">{roleLabel(m.role, i18n)}</span>
              <span className="text-muted-foreground">{scopeName(m)}</span>
              <span className="text-muted-foreground">
                <SiteDate value={m.valid_from} timeZone={tz} />
                {' – '}
                {m.valid_to ? (
                  <SiteDate value={m.valid_to} timeZone={tz} />
                ) : (
                  <Trans>open-ended</Trans>
                )}
              </span>
              {m.site_owned ? (
                <Badge variant="info">
                  <Trans>Owned by the site</Trans>
                </Badge>
              ) : null}
              {canEdit ? (
                <Button
                  variant="ghost"
                  size="sm"
                  className="ms-auto"
                  onClick={() => setRemoving(m)}
                >
                  <Trans>Remove</Trans>
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {canEdit ? <AddMembership orgId={orgId} personId={personId} onAdded={invalidate} /> : null}
      <Dialog open={!!removing} onOpenChange={(o) => !o && setRemoving(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              <Trans>Remove this role?</Trans>
            </DialogTitle>
            <DialogDescription>
              {removing?.site_owned ? (
                <Trans>
                  This role belongs to the site, so you will be asked to confirm it is you.
                </Trans>
              ) : (
                <Trans>The person loses the access this role gave them.</Trans>
              )}
            </DialogDescription>
          </DialogHeader>
          {remove.error ? (
            <FormError message={t`The role could not be removed. Please try again.`} />
          ) : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => setRemoving(null)}>
              <Trans>Cancel</Trans>
            </Button>
            <Button
              variant="destructive"
              disabled={remove.isPending}
              onClick={() => removing && remove.mutate(removing)}
            >
              <Trans>Remove role</Trans>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function AddMembership({
  orgId,
  personId,
  onAdded,
}: {
  orgId: string
  personId: string
  onAdded: () => void
}) {
  const { t, i18n } = useLingui()
  const { org } = useSession()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const sites = org?.sites ?? []
  const schema = useMemo(
    () =>
      z.object({
        role: z.string(),
        scope_type: z.string(),
        scope_id: z.string(),
        valid_from: z.string().min(1, t`Choose the start date`),
        valid_to: z.string(),
      }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: {
      role: 'site_manager',
      scope_type: 'site',
      scope_id: sites[0]?.id ?? '',
      valid_from: new Date().toISOString().slice(0, 10),
      valid_to: '',
    },
  })
  const scope = form.watch('scope_type')
  const add = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/memberships', {
          params: { path: { org_id: orgId } },
          body: {
            person_id: personId,
            role: v.role as 'staff',
            scope_type: v.scope_type as 'site',
            scope_id: v.scope_type === 'org' ? null : v.scope_id,
            valid_from: v.valid_from,
            valid_to: v.valid_to || null,
          },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: () => {
      idem.renew()
      onAdded()
    },
    onError: (e) => applyError(form, e),
  })
  const { errors } = form.formState
  return (
    <form
      noValidate
      aria-label={t`Give a role`}
      className="grid max-w-2xl gap-3 rounded-md border p-4 sm:grid-cols-2"
      onSubmit={form.handleSubmit((v) => add.mutateAsync(v).catch(() => undefined))}
    >
      <h3 className="text-sm font-semibold sm:col-span-2">
        <Trans>Give a role</Trans>
      </h3>
      <Field id="mem-role" label={<Trans>Role</Trans>}>
        <Select id="mem-role" {...form.register('role')}>
          {ROLES.map((r) => (
            <option key={r} value={r}>
              {roleLabel(r, i18n)}
            </option>
          ))}
        </Select>
      </Field>
      <Field id="mem-scope" label={<Trans>Applies to</Trans>}>
        <Select id="mem-scope" {...form.register('scope_type')}>
          <option value="site">{scopeLabel('site', i18n)}</option>
          <option value="org">{scopeLabel('org', i18n)}</option>
        </Select>
      </Field>
      {scope !== 'org' ? (
        <Field id="mem-site" label={<Trans>Site</Trans>} error={errors.scope_id?.message}>
          <Select id="mem-site" {...form.register('scope_id')}>
            {sites.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        </Field>
      ) : null}
      <Field id="mem-from" label={<Trans>From</Trans>} error={errors.valid_from?.message}>
        <Input
          id="mem-from"
          type="date"
          aria-invalid={!!errors.valid_from}
          {...form.register('valid_from')}
        />
      </Field>
      <Field id="mem-to" label={<Trans>Until (optional)</Trans>}>
        <Input id="mem-to" type="date" {...form.register('valid_to')} />
      </Field>
      <div className="flex flex-col gap-3 sm:col-span-2">
        <FormError message={errors.root?.message} />
        <div>
          <SubmitButton busy={add.isPending}>
            <Trans>Give role</Trans>
          </SubmitButton>
        </div>
      </div>
    </form>
  )
}
