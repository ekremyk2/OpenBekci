import { lazy, Suspense, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { ArchiveIcon, ArrowLeftIcon, PencilIcon, Trash2Icon } from 'lucide-react'
import { apiDraft, ifMatch } from '@/api/draft'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { SiteDate } from '@/components/data/SiteDate'
import { AttachmentGallery } from '@/components/files/AttachmentGallery'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { useSite } from '@/features/dashboard/site'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { LoadMore, must, useCursorQuery } from '@/lib/pagination'
import { toast } from '@/lib/toast'
import { StatusLabel } from './AnnouncementsPage'

const MarkdownView = lazy(() => import('./Markdown'))

function Reads({
  orgId,
  announcementId,
  timeZone,
}: {
  orgId: string
  announcementId: string
  timeZone: string
}) {
  const { t } = useLingui()
  const list = useCursorQuery(
    ['operations.listAnnouncementReads', orgId, announcementId],
    async (cursor) =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/announcements/{announcement_id}/reads', {
          params: {
            path: { org_id: orgId, announcement_id: announcementId },
            query: { limit: 50, cursor },
          },
        }),
      ),
  )
  const first = list.data?.pages[0] as unknown as
    { read_count?: number; audience_count?: number } | undefined
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Read receipts</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {list.isPending ? (
          <PageSkeleton />
        ) : list.isError ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : (
          <>
            <p className="text-sm">
              <Trans>
                {first?.read_count ?? 0} of {first?.audience_count ?? 0} have opened this
                announcement. A delivered SMS or email is not counted as read.
              </Trans>
            </p>
            <ul className="flex flex-col divide-y rounded-md border" aria-label={t`Readers`}>
              {list.items.map((r) => (
                <li key={r.person_id} className="flex items-center gap-2 p-2 text-sm">
                  <span className="me-auto">
                    {r.display_name}
                    {r.unit_door_numbers.length ? ` (${r.unit_door_numbers.join(', ')})` : ''}
                  </span>
                  {r.read_at ? (
                    <SiteDate
                      value={r.read_at}
                      timeZone={timeZone}
                      kind="datetime"
                      className="text-xs text-muted-foreground"
                    />
                  ) : (
                    <Badge variant="outline">
                      <Trans>Not read</Trans>
                    </Badge>
                  )}
                </li>
              ))}
            </ul>
            <LoadMore query={list} />
          </>
        )}
      </CardContent>
    </Card>
  )
}

export function AnnouncementDetailPage({ announcementId }: { announcementId: string }) {
  const { t } = useLingui()
  const { org, can } = useSession()
  const { site } = useSite()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const orgId = org?.id ?? ''
  const tz = site?.timezone ?? 'UTC'
  const canPublish = can('announcements.publish')
  const publishKey = useIdempotencyKey()

  const query = useQuery({
    queryKey: ['operations.getAnnouncement', orgId, announcementId],
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/announcements/{announcement_id}', {
          params: { path: { org_id: orgId, announcement_id: announcementId } },
        }),
      ),
  })
  const a = query.data

  // Opening a published announcement counts as a read (O4); delivery alone does not.
  const markRead = useMutation({
    mutationFn: async () =>
      apiDraft.PUT('/v1/orgs/{org_id}/announcements/{announcement_id}/read', {
        params: { path: { org_id: orgId, announcement_id: announcementId } },
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['operations.listAnnouncements'] }),
  })
  const needsRead = !!a && a.status === 'published' && !a.read_at && !canPublish
  useEffect(() => {
    if (needsRead && !markRead.isPending && !markRead.isSuccess) markRead.mutate()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [needsRead])

  const refresh = () =>
    Promise.all([
      qc.invalidateQueries({ queryKey: ['operations.getAnnouncement'] }),
      qc.invalidateQueries({ queryKey: ['operations.listAnnouncements'] }),
    ])

  const publish = useMutation({
    mutationFn: async () =>
      must(
        await apiDraft.POST('/v1/orgs/{org_id}/announcements/{announcement_id}:publish', {
          params: { path: { org_id: orgId, announcement_id: announcementId } },
          body: {},
          ...withIdempotencyKey(publishKey.key),
        }),
      ),
    onSuccess: refresh,
    onError: () => toast.error(t`Could not publish. Please try again.`),
  })
  const archive = useMutation({
    mutationFn: async () =>
      must(
        await apiDraft.POST('/v1/orgs/{org_id}/announcements/{announcement_id}:archive', {
          params: { path: { org_id: orgId, announcement_id: announcementId } },
          ...withIdempotencyKey(crypto.randomUUID()),
        }),
      ),
    onSuccess: refresh,
  })
  const remove = useMutation({
    mutationFn: async () =>
      apiDraft.DELETE('/v1/orgs/{org_id}/announcements/{announcement_id}', {
        params: {
          path: { org_id: orgId, announcement_id: announcementId },
          header: ifMatch(a?.version ?? 0),
        },
      }),
    onSuccess: async () => {
      await refresh()
      await navigate({ to: '/orgs/$orgId/announcements', params: { orgId } })
    },
  })

  if (query.isPending) return <PageSkeleton />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const x = query.data

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4">
      <Link
        to="/orgs/$orgId/announcements"
        params={{ orgId }}
        className="inline-flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeftIcon className="size-4 rtl-flip" aria-hidden="true" />
        <Trans>All announcements</Trans>
      </Link>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="me-auto text-2xl font-semibold">{x.title}</h1>
        {canPublish ? <StatusLabel status={x.status} /> : null}
      </div>
      <p className="text-sm text-muted-foreground">
        <SiteDate
          value={x.published_at ?? x.publish_at ?? x.created_at}
          timeZone={tz}
          kind="datetime"
        />
      </p>

      {canPublish ? (
        <div className="flex flex-wrap gap-2">
          {x.status === 'draft' ? (
            <Button disabled={publish.isPending} onClick={() => publish.mutate()}>
              <Trans>Publish now</Trans>
            </Button>
          ) : null}
          {x.status !== 'archived' ? (
            <Link
              to="/orgs/$orgId/announcements/$announcementId/edit"
              params={{ orgId, announcementId }}
              className={buttonVariants({ variant: 'outline' })}
            >
              <PencilIcon aria-hidden="true" />
              <Trans>Edit</Trans>
            </Link>
          ) : null}
          {x.status === 'published' ? (
            <Button variant="outline" disabled={archive.isPending} onClick={() => archive.mutate()}>
              <ArchiveIcon aria-hidden="true" />
              <Trans>Archive</Trans>
            </Button>
          ) : null}
          {x.status === 'draft' || x.status === 'scheduled' ? (
            <Button
              variant="ghost"
              disabled={remove.isPending}
              onClick={() => {
                if (window.confirm(t`Delete this announcement?`)) remove.mutate()
              }}
            >
              <Trash2Icon aria-hidden="true" />
              <Trans>Delete</Trans>
            </Button>
          ) : null}
        </div>
      ) : null}

      <Card>
        <CardContent className="flex flex-col gap-4 pt-6">
          <Suspense fallback={<Skeleton className="h-24 w-full" />}>
            <MarkdownView source={x.body_md} />
          </Suspense>
          <AttachmentGallery
            orgId={orgId}
            items={x.file_ids.map((id) => ({ id, fileId: id, mime: 'application/octet-stream' }))}
          />
        </CardContent>
      </Card>

      {canPublish && x.status === 'published' ? (
        <>
          <p className="text-sm text-muted-foreground">
            <Trans>
              Sent to {x.recipient_count ?? 0} people, {x.sms_count ?? 0} SMS messages.
            </Trans>
          </p>
          <Reads orgId={orgId} announcementId={announcementId} timeZone={tz} />
        </>
      ) : null}
    </div>
  )
}
