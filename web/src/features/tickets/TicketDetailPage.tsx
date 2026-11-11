import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Trans } from '@lingui/react/macro'
import { ArrowLeftIcon } from 'lucide-react'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { SiteDate } from '@/components/data/SiteDate'
import { PageSkeleton } from '@/components/data/Skeletons'
import { AttachmentGallery } from '@/components/files/AttachmentGallery'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useSite } from '@/features/dashboard/site'
import { StatusBadge, VisibilityBadge } from './badges'
import { commentsQuery, historyQuery, ticketQuery, useMyPersonId, useWorkflow } from './queries'
import {
  AssigneeEditor,
  MergeDialog,
  StatusChanger,
  VisibilityEditor,
  VoteButton,
} from './TicketActions'
import { CommentForm, Timeline } from './TicketThread'

export function TicketDetailPage({ ticketId }: { ticketId: string }) {
  const { org, can } = useSession()
  const { sites } = useSite()
  const orgId = org?.id ?? ''
  const ticket = useQuery(ticketQuery(orgId, ticketId))
  const comments = useQuery(commentsQuery(orgId, ticketId))
  const history = useQuery(historyQuery(orgId, ticketId))
  const siteId = ticket.data?.site_id
  const workflow = useWorkflow(orgId, siteId)
  const timeZone = sites.find((s) => s.id === siteId)?.timezone ?? 'UTC'

  const canManage = can('tickets.manage')
  const canWork = can('tickets.work')
  const canInternal = canManage || canWork || can('tickets.read')
  const canOpen = can('tickets.open')
  // `tickets.work` is "assigned tickets": a work-only user acts on the tickets assigned to them
  const workOnly = canWork && !canManage
  const me = useMyPersonId(orgId, siteId, workOnly)

  if (ticket.isPending) return <PageSkeleton />
  if (ticket.isError) {
    return <ErrorState error={ticket.error} onRetry={() => void ticket.refetch()} />
  }
  const t = ticket.data
  const closed = t.status_semantic === 'closed_like'
  const assignedToMe = !!me.data && t.assignee?.person_id === me.data
  const canChangeStatus = canManage || (canWork && assignedToMe)

  return (
    <div className="flex flex-col gap-4">
      <Link
        to="/orgs/$orgId/tickets"
        params={{ orgId }}
        className="inline-flex min-h-11 w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeftIcon className="size-4 rtl-flip" aria-hidden="true" />
        <Trans>All tickets</Trans>
      </Link>

      <div className="flex flex-wrap items-start gap-2">
        <h1 className="me-auto text-2xl font-semibold">{t.title}</h1>
        <StatusBadge name={t.status_name} semantic={t.status_semantic} />
        <VisibilityBadge visibility={t.visibility} />
      </div>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Opened by {t.reporter_display_name}
          {t.unit_door_number ? ` (${t.unit_door_number})` : ''}
        </Trans>{' '}
        <SiteDate value={t.created_at} timeZone={timeZone} kind="datetime" />
      </p>
      {t.merged_into_id ? (
        <p role="status" className="rounded-md border border-dashed p-3 text-sm">
          <Link
            to="/orgs/$orgId/tickets/$ticketId"
            params={{ orgId, ticketId: t.merged_into_id }}
            className="text-primary underline-offset-4 hover:underline"
          >
            <Trans>This ticket was merged into another one.</Trans>
          </Link>
        </p>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-[1fr_20rem]">
        <div className="flex min-w-0 flex-col gap-4">
          <Card>
            <CardContent className="flex flex-col gap-3 pt-6">
              <p className="whitespace-pre-wrap">{t.description}</p>
              <AttachmentGallery
                orgId={orgId}
                items={t.attachments
                  .filter((a) => !a.comment_id)
                  .map((a) => ({ id: a.id, fileId: a.file_id, mime: a.mime }))}
              />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>
                <Trans>Activity</Trans>
              </CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              {comments.isPending || history.isPending ? (
                <PageSkeleton />
              ) : comments.isError || history.isError ? (
                <ErrorState
                  error={comments.error ?? history.error}
                  onRetry={() => {
                    void comments.refetch()
                    void history.refetch()
                  }}
                />
              ) : (
                <Timeline
                  orgId={orgId}
                  timeZone={timeZone}
                  comments={comments.data}
                  history={history.data}
                  statuses={workflow.data?.statuses ?? []}
                />
              )}
              {canOpen || canWork || canManage ? (
                <CommentForm
                  orgId={orgId}
                  siteId={t.site_id}
                  ticket={t}
                  canInternal={canInternal}
                />
              ) : null}
            </CardContent>
          </Card>
        </div>

        <div className="flex flex-col gap-4">
          <Card>
            <CardContent className="flex flex-col gap-3 pt-6">
              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
                <dt className="text-muted-foreground">
                  <Trans>Assigned to</Trans>
                </dt>
                <dd>{t.assignee?.display_name ?? <Trans>Unassigned</Trans>}</dd>
                <dt className="text-muted-foreground">
                  <Trans>Confirmations</Trans>
                </dt>
                <dd>+{t.vote_count}</dd>
                {closed && t.closed_at ? (
                  <>
                    <dt className="text-muted-foreground">
                      <Trans>Closed</Trans>
                    </dt>
                    <dd>
                      <SiteDate value={t.closed_at} timeZone={timeZone} kind="datetime" />
                    </dd>
                  </>
                ) : null}
              </dl>
              {t.visibility === 'common' && canOpen && !t.merged_into_id ? (
                <VoteButton orgId={orgId} ticket={t} />
              ) : null}
            </CardContent>
          </Card>

          {canChangeStatus && workflow.data && !t.merged_into_id ? (
            <Card>
              <CardContent className="pt-6">
                <StatusChanger
                  orgId={orgId}
                  ticket={t}
                  workflow={workflow.data}
                  canInternal={canInternal}
                />
              </CardContent>
            </Card>
          ) : null}

          {canManage ? (
            <Card>
              <CardContent className="flex flex-col gap-4 pt-6">
                <AssigneeEditor orgId={orgId} ticket={t} />
                <VisibilityEditor orgId={orgId} ticket={t} />
                <MergeDialog orgId={orgId} siteId={t.site_id} ticket={t} />
              </CardContent>
            </Card>
          ) : null}
        </div>
      </div>
    </div>
  )
}
