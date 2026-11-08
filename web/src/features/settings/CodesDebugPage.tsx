import { useQuery } from '@tanstack/react-query'
import { Trans } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { must } from '@/lib/pagination'
import { DEBUG } from './SettingsLayout'

/** Server-side labels for open enums, problem codes and ticket statuses; developers only. */
export function CodesDebugPage() {
  const { org, can } = useSession()
  const orgId = org?.id ?? ''
  const allowed = DEBUG && can('codes.read')
  const query = useQuery({
    queryKey: ['codes.listCodes', orgId],
    enabled: allowed,
    queryFn: async () =>
      must(await draftApi.GET('/v1/orgs/{org_id}/codes', { params: { path: { org_id: orgId } } })),
  })
  if (!allowed) {
    return <EmptyState title={<Trans>Not available</Trans>} />
  }
  if (query.isPending) return <PageSkeleton />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        <Trans>Debug view of /codes for locale {query.data.locale}.</Trans>
      </p>
      <pre
        className="max-h-[60dvh] overflow-auto rounded-md border bg-muted/40 p-3 text-xs"
        dir="ltr"
      >
        {JSON.stringify(query.data, null, 2)}
      </pre>
    </div>
  )
}
