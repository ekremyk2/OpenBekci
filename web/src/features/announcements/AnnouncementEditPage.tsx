import { useQuery } from '@tanstack/react-query'
import { draftApi } from '@/api/draft'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { must } from '@/lib/pagination'
import { AnnouncementForm } from './AnnouncementForm'

export function AnnouncementEditPage({ announcementId }: { announcementId: string }) {
  const { org } = useSession()
  const orgId = org?.id ?? ''
  const query = useQuery({
    queryKey: ['operations.getAnnouncement', orgId, announcementId],
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/announcements/{announcement_id}', {
          params: { path: { org_id: orgId, announcement_id: announcementId } },
        }),
      ),
  })
  if (query.isPending) return <PageSkeleton />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  return <AnnouncementForm key={query.data.version} existing={query.data} />
}
