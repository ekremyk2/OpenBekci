import { Link } from '@tanstack/react-router'
import { AccessDenied } from '@/components/data/AccessDenied'
import { Trans, useLingui } from '@lingui/react/macro'
import { EyeIcon, MegaphoneIcon, PinIcon, PlusIcon } from 'lucide-react'
import { draftApi, type Announcement } from '@/api/draft'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { SiteDate } from '@/components/data/SiteDate'
import { TableSkeleton } from '@/components/data/Skeletons'
import { Badge } from '@/components/ui/badge'
import { buttonVariants } from '@/components/ui/button'
import { Select } from '@/components/ui/select'
import { SitePicker, useSite } from '@/features/dashboard/site'
import { LoadMore, must, useCursorQuery } from '@/lib/pagination'

export type AnnouncementFilter = 'all' | 'draft' | 'scheduled' | 'published' | 'archived'

export function StatusLabel({ status }: { status: string }) {
  switch (status) {
    case 'draft':
      return (
        <Badge variant="outline">
          <Trans>Draft</Trans>
        </Badge>
      )
    case 'scheduled':
      return (
        <Badge variant="info">
          <Trans>Scheduled</Trans>
        </Badge>
      )
    case 'published':
      return (
        <Badge variant="success">
          <Trans>Published</Trans>
        </Badge>
      )
    case 'archived':
      return (
        <Badge variant="secondary">
          <Trans>Archived</Trans>
        </Badge>
      )
    default:
      return <Badge variant="outline">{status}</Badge>
  }
}

export function AnnouncementsPage({
  status,
  onStatus,
}: {
  status: AnnouncementFilter
  onStatus: (s: AnnouncementFilter) => void
}) {
  const { t } = useLingui()
  const { org, can } = useSession()
  const { site } = useSite()
  const orgId = org?.id ?? ''
  const canPublish = can('announcements.publish')
  const tz = site?.timezone ?? 'UTC'

  const list = useCursorQuery<Announcement>(
    ['operations.listAnnouncements', orgId, site?.id, status],
    async (cursor) =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/announcements', {
          params: {
            path: { org_id: orgId },
            query: {
              site_id: site!.id,
              status: status === 'all' ? undefined : status,
              limit: 25,
              cursor,
            },
          },
        }),
      ),
    { enabled: !!site && (can('announcements.read') || canPublish) },
  )

  if (!can('announcements.read') && !canPublish) {
    return <AccessDenied />
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="me-auto text-2xl font-semibold">
          <Trans>Announcements</Trans>
        </h1>
        <SitePicker />
        {canPublish ? (
          <>
            <Select
              className="w-40"
              aria-label={t`Status`}
              value={status}
              onChange={(e) => onStatus(e.target.value as AnnouncementFilter)}
            >
              <option value="all">{t`All statuses`}</option>
              <option value="draft">{t`Drafts`}</option>
              <option value="scheduled">{t`Scheduled`}</option>
              <option value="published">{t`Published`}</option>
              <option value="archived">{t`Archived`}</option>
            </Select>
            <Link
              to="/orgs/$orgId/announcements/new"
              params={{ orgId }}
              className={buttonVariants()}
            >
              <PlusIcon aria-hidden="true" />
              <Trans>New announcement</Trans>
            </Link>
          </>
        ) : null}
      </div>

      {list.isPending ? (
        <TableSkeleton rows={4} columns={2} />
      ) : list.isError ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : list.items.length === 0 ? (
        <EmptyState
          icon={MegaphoneIcon}
          title={<Trans>No announcements</Trans>}
          description={
            canPublish ? (
              <Trans>Write the first announcement for this site.</Trans>
            ) : (
              <Trans>Notices from your management will appear here.</Trans>
            )
          }
        />
      ) : (
        <>
          <ul className="flex flex-col divide-y rounded-lg border" aria-label={t`Announcements`}>
            {list.items.map((a) => (
              <li key={a.id}>
                <Link
                  to="/orgs/$orgId/announcements/$announcementId"
                  params={{ orgId, announcementId: a.id }}
                  className="flex flex-col gap-1 p-3 outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                >
                  <div className="flex flex-wrap items-center gap-2">
                    {a.pinned ? (
                      <PinIcon className="size-4 text-muted-foreground" aria-label={t`Pinned`} />
                    ) : null}
                    <span className={a.read_at || canPublish ? 'font-medium' : 'font-semibold'}>
                      {a.title}
                    </span>
                    {canPublish ? <StatusLabel status={a.status} /> : null}
                    {!canPublish && !a.read_at ? (
                      <Badge variant="info">
                        <Trans>New</Trans>
                      </Badge>
                    ) : null}
                  </div>
                  <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
                    <SiteDate
                      value={a.published_at ?? a.publish_at ?? a.created_at}
                      timeZone={tz}
                      kind="datetime"
                    />
                    {canPublish && a.status === 'published' ? (
                      <span className="inline-flex items-center gap-1">
                        <EyeIcon className="size-3" aria-hidden="true" />
                        <Trans>
                          {a.read_count} of {a.recipient_count ?? '?'} read
                        </Trans>
                      </span>
                    ) : null}
                  </div>
                </Link>
              </li>
            ))}
          </ul>
          <LoadMore query={list} />
        </>
      )}
    </div>
  )
}
