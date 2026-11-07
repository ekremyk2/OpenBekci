import { useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { InboxIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { occupancyKindLabel } from '@/lib/labels'
import { DataTable, ListEmpty, useCursorPages } from '@/components/data/DataTable'
import { SiteDate } from '@/components/data/SiteDate'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { PageHeader } from '@/components/layout/PageHeader'
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
import { claimStatusLabel } from './labels'
import { PeopleNav } from './PeopleNav'
import { personQueries } from './queries'

type Claim = Schema<'UnitClaim'>

/** Approval queue of unit claims made through the site QR code or join code (S4). */
export function ClaimsPage({ status }: { status: string }) {
  const { org } = useSession()
  const { t, i18n } = useLingui()
  const navigate = useNavigate()
  const pages = useCursorPages()
  const orgId = org?.id ?? ''
  const tz = org?.sites[0]?.timezone ?? 'UTC'
  const query = useQuery({
    ...personQueries.claims(orgId, { status: status || undefined, cursor: pages.cursor }),
    enabled: !!org,
  })
  const [deciding, setDeciding] = useState<{ claim: Claim; mode: 'approve' | 'reject' } | null>(
    null,
  )

  const columns: ColumnDef<Claim, unknown>[] = [
    {
      header: t`Who`,
      cell: ({ row }) => <span className="font-medium">{row.original.display_name}</span>,
    },
    { header: t`Unit`, cell: ({ row }) => row.original.unit_door_number },
    {
      header: t`Claims to be`,
      cell: ({ row }) => occupancyKindLabel(row.original.claimed_role, i18n),
    },
    { header: t`Note`, cell: ({ row }) => row.original.note ?? '–' },
    {
      header: t`Asked`,
      cell: ({ row }) => <SiteDate value={row.original.created_at} timeZone={tz} />,
    },
    {
      header: t`Decision`,
      cell: ({ row }) =>
        row.original.status === 'pending' ? (
          <span className="flex gap-1">
            <Button size="sm" onClick={() => setDeciding({ claim: row.original, mode: 'approve' })}>
              <Trans>Approve</Trans>
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => setDeciding({ claim: row.original, mode: 'reject' })}
            >
              <Trans>Reject</Trans>
            </Button>
          </span>
        ) : (
          <Badge variant={row.original.status === 'approved' ? 'success' : 'secondary'}>
            {claimStatusLabel(row.original.status, i18n)}
          </Badge>
        ),
    },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={<Trans>Unit claims</Trans>}
        description={
          <Trans>
            Residents who joined with the site QR code and say they live in a unit. A claim takes
            effect only when you approve it.
          </Trans>
        }
      />
      <PeopleNav />
      <Select
        aria-label={t`Show`}
        className="w-auto self-start"
        value={status}
        onChange={(e) => {
          pages.reset()
          void navigate({ to: '.', search: { status: e.target.value } as never, replace: true })
        }}
      >
        <option value="pending">{t`Waiting for a decision`}</option>
        <option value="approved">{t`Approved`}</option>
        <option value="rejected">{t`Rejected`}</option>
        <option value="">{t`All`}</option>
      </Select>
      <DataTable
        caption={t`Unit claims`}
        columns={columns}
        data={query.data?.items}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => void query.refetch()}
        getRowId={(c) => c.id}
        pager={{
          page: pages.page,
          hasPrev: pages.hasPrev,
          hasNext: !!query.data?.next_cursor,
          onPrev: pages.prev,
          onNext: () => query.data?.next_cursor && pages.next(query.data.next_cursor),
        }}
        empty={
          <ListEmpty
            filtered={status !== 'pending'}
            icon={InboxIcon}
            title={<Trans>No claims waiting</Trans>}
            description={
              <Trans>When a resident joins with the site QR code, their claim appears here.</Trans>
            }
          />
        }
      />
      {deciding ? (
        <DecisionDialog orgId={orgId} {...deciding} onClose={() => setDeciding(null)} />
      ) : null}
    </div>
  )
}

function DecisionDialog({
  orgId,
  claim,
  mode,
  onClose,
}: {
  orgId: string
  claim: Claim
  mode: 'approve' | 'reject'
  onClose: () => void
}) {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const approve = mode === 'approve'
  const schema = useMemo(
    () =>
      z.object({
        share_pct: z
          .string()
          .refine(
            (v) => v === '' || (Number(v) > 0 && Number(v) <= 100),
            t`Enter a share between 1 and 100`,
          ),
        valid_from: z.string(),
        reason: approve
          ? z.string()
          : z
              .string()
              .trim()
              .min(1, t`Give the person a reason`),
      }),
    [t, approve],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { share_pct: '', valid_from: new Date().toISOString().slice(0, 10), reason: '' },
  })
  const decide = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(() =>
        approve
          ? draftApi.POST('/v1/orgs/{org_id}/unit-claims/{claim_id}:approve', {
              params: { path: { org_id: orgId, claim_id: claim.id } },
              body: {
                share_pct: v.share_pct ? Number(v.share_pct) : undefined,
                valid_from: v.valid_from || undefined,
                note: v.reason || undefined,
              },
              ...withIdempotencyKey(idem.key),
            })
          : draftApi.POST('/v1/orgs/{org_id}/unit-claims/{claim_id}:reject', {
              params: { path: { org_id: orgId, claim_id: claim.id } },
              body: { reason: v.reason },
              ...withIdempotencyKey(idem.key),
            }),
      ),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['people.listUnitClaims', orgId] })
      onClose()
    },
    onError: (e) => applyError(form, e),
  })
  const { errors } = form.formState
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form
          noValidate
          className="grid gap-4"
          onSubmit={form.handleSubmit((v) => decide.mutateAsync(v).catch(() => undefined))}
        >
          <DialogHeader>
            <DialogTitle>
              {approve ? (
                <Trans>Approve {claim.display_name}</Trans>
              ) : (
                <Trans>Reject {claim.display_name}</Trans>
              )}
            </DialogTitle>
            <DialogDescription>
              <Trans>
                Claims {occupancyKindLabel(claim.claimed_role, i18n)} of {claim.unit_door_number}.
              </Trans>
            </DialogDescription>
          </DialogHeader>
          {approve ? (
            <>
              {claim.claimed_role === 'owner' ? (
                <Field
                  id="claim-share"
                  label={<Trans>Ownership share (%)</Trans>}
                  error={errors.share_pct?.message}
                >
                  <Input
                    id="claim-share"
                    inputMode="decimal"
                    aria-invalid={!!errors.share_pct}
                    {...form.register('share_pct')}
                  />
                </Field>
              ) : null}
              <Field id="claim-from" label={<Trans>Starts on</Trans>}>
                <Input id="claim-from" type="date" {...form.register('valid_from')} />
              </Field>
            </>
          ) : null}
          <Field
            id="claim-reason"
            label={approve ? <Trans>Note (optional)</Trans> : <Trans>Reason</Trans>}
            error={errors.reason?.message}
          >
            <Input id="claim-reason" aria-invalid={!!errors.reason} {...form.register('reason')} />
          </Field>
          <FormError message={errors.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <SubmitButton busy={decide.isPending} variant={approve ? 'default' : 'destructive'}>
              {approve ? <Trans>Approve claim</Trans> : <Trans>Reject claim</Trans>}
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
