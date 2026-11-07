import { useInfiniteQuery, type QueryKey } from '@tanstack/react-query'
import { Trans } from '@lingui/react/macro'
import { LoaderCircleIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'

export interface Page<T> {
  items: T[]
  next_cursor: string | null
}

/** The data of a successful openapi-fetch result (the client middleware throws on problems). */
export function must<T>(result: { data?: T }): T {
  if (result.data === undefined) throw new Error('Empty response')
  return result.data
}

/**
 * Feed-style cursor pagination (`limit`, `cursor` → `next_cursor`, web plan §6). Reading the first
 * page again after a mutation is done by invalidating `key`.
 */
export function useCursorQuery<T>(
  key: QueryKey,
  fetchPage: (cursor: string | undefined) => Promise<Page<T>>,
  options: { enabled?: boolean; refetchInterval?: number } = {},
) {
  const query = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam }) => fetchPage(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: options.enabled,
    refetchInterval: options.refetchInterval,
  })
  const items = query.data?.pages.flatMap((p) => p.items) ?? []
  return { ...query, items }
}

export function LoadMore({
  query,
}: {
  query: { hasNextPage: boolean; isFetchingNextPage: boolean; fetchNextPage: () => unknown }
}) {
  if (!query.hasNextPage) return null
  return (
    <div className="flex justify-center py-2">
      <Button
        variant="outline"
        onClick={() => void query.fetchNextPage()}
        disabled={query.isFetchingNextPage}
      >
        {query.isFetchingNextPage ? (
          <LoaderCircleIcon className="animate-spin" aria-hidden="true" />
        ) : null}
        <Trans>Load more</Trans>
      </Button>
    </div>
  )
}
