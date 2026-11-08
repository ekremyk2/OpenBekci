import { CheckIcon, XIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { toast } from '@/lib/toast'
import {
  gApi,
  idemHeader,
  unwrap,
  useKeyStore,
  useOrgMutation,
  useOrgQuery,
  type S,
  type SiteCtx,
} from '@/features/meetings/kit/api'
import { QueryBoundary } from '@/features/meetings/kit/ui'

export const APPROVAL_POLL_MS = 5000

/** Pending approval requests of the caller's units, refreshed while the page is open. */
export function usePendingApprovals(ctx: SiteCtx, enabled = true) {
  return useOrgQuery(
    ['approvals', 'pending'],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/visitor-approval-requests', {
          params: { path: { org_id: ctx.orgId }, query: { status: 'pending', limit: 50 } },
        }),
      ).then((r) => r.items),
    { enabled, refetchInterval: APPROVAL_POLL_MS },
  )
}

/** The little number next to "Approvals": how many requests wait for an answer. */
export function ApprovalBadge({ ctx, enabled }: { ctx: SiteCtx; enabled: boolean }) {
  const { t } = useLingui()
  const q = usePendingApprovals(ctx, enabled)
  const n = q.data?.length ?? 0
  if (n === 0) return null
  return (
    <Badge variant="destructive" aria-label={t`${n} requests waiting`} data-testid="approval-badge">
      {n}
    </Badge>
  )
}

function RequestRow({ ctx, r }: { ctx: SiteCtx; r: S['VisitorApprovalRequest'] }) {
  const { t } = useLingui()
  const keys = useKeyStore()
  const decide = useOrgMutation(async (decision: 'approve' | 'deny') => {
    const id = `${r.id}-${decision}`
    const res = await unwrap(
      gApi.POST('/v1/orgs/{org_id}/visitor-approval-requests/{request_id}:decide', {
        params: { path: { org_id: ctx.orgId, request_id: r.id }, header: idemHeader(keys.get(id)) },
        body: { decision },
      }),
    )
    keys.done(id)
    return res
  })
  const answer = (d: 'approve' | 'deny') =>
    decide.mutate(d, {
      onSuccess: () => toast.info(d === 'approve' ? t`Visitor approved.` : t`Visitor denied.`),
      onError: () =>
        toast.error(t`Could not send your answer. It may already have been answered or expired.`),
    })
  return (
    <li className="flex flex-wrap items-center gap-3 rounded-lg border p-4">
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="text-base font-semibold">{r.visitor_name}</span>
        <span className="text-sm text-muted-foreground">
          <Trans>At the gate for {r.unit_door_number}</Trans>
          {r.plate ? <span dir="ltr"> · {r.plate}</span> : null}
        </span>
        <span className="text-xs text-muted-foreground">
          <SiteDate value={r.requested_at} timeZone={ctx.timeZone} kind="time" />
        </span>
      </div>
      {r.status === 'pending' ? (
        <div className="flex gap-2">
          <Button size="lg" disabled={decide.isPending} onClick={() => answer('approve')}>
            <CheckIcon />
            <Trans>Approve</Trans>
          </Button>
          <Button
            size="lg"
            variant="destructive"
            disabled={decide.isPending}
            onClick={() => answer('deny')}
          >
            <XIcon />
            <Trans>Deny</Trans>
          </Button>
        </div>
      ) : (
        <Badge variant={r.status === 'approved' ? 'success' : 'secondary'}>
          {r.status === 'approved' ? t`Approved` : r.status === 'denied' ? t`Denied` : t`Expired`}
        </Badge>
      )}
    </li>
  )
}

/** Approve or deny walk-in visitors the guard asks about (C2). */
export function ApprovalInbox({ ctx }: { ctx: SiteCtx }) {
  const { t } = useLingui()
  const pending = usePendingApprovals(ctx)
  const recent = useOrgQuery(['approvals', 'recent'], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/visitor-approval-requests', {
        params: { path: { org_id: ctx.orgId }, query: { limit: 10 } },
      }),
    ).then((r) => r.items.filter((x) => x.status !== 'pending')),
  )
  return (
    <div className="flex flex-col gap-4">
      <QueryBoundary
        query={pending}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No visitor is waiting`}
        emptyDescription={t`When the guard asks about an unexpected visitor, the request shows up here and by SMS. This page checks every few seconds.`}
        skeleton="page"
      >
        {(items) => (
          <ul className="flex flex-col gap-3" aria-label={t`Requests waiting`}>
            {items.map((r) => (
              <RequestRow key={r.id} ctx={ctx} r={r} />
            ))}
          </ul>
        )}
      </QueryBoundary>
      {(recent.data ?? []).length > 0 ? (
        <section className="flex flex-col gap-2">
          <h2 className="text-sm font-medium text-muted-foreground">
            <Trans>Answered recently</Trans>
          </h2>
          <ul className="flex flex-col gap-2">
            {(recent.data ?? []).map((r) => (
              <RequestRow key={r.id} ctx={ctx} r={r} />
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  )
}
