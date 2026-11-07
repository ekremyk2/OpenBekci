import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useLingui } from '@lingui/react/macro'
import { BellIcon } from 'lucide-react'
import { apiDraft } from '@/api/draft'
import { buttonVariants } from '@/components/ui/button'
import { must } from '@/lib/pagination'

export const UNREAD_POLL_MS = 60_000

/**
 * Unread badge in the top bar. Polls the unread-count endpoint every 60 s; TanStack Query pauses
 * the interval while the tab is hidden (`refetchIntervalInBackground` stays off), web plan §6.
 */
export function useUnreadCount(orgId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: ['operations.getUnreadCount', orgId],
    enabled: !!orgId && enabled,
    refetchInterval: UNREAD_POLL_MS,
    refetchIntervalInBackground: false,
    staleTime: 0,
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/me/notifications/unread-count', {
          params: { path: { org_id: orgId! } },
        }),
      ),
  })
}

export function NotificationBell({ orgId }: { orgId: string }) {
  const { t } = useLingui()
  const { data } = useUnreadCount(orgId)
  const unread = data?.unread ?? 0
  return (
    <Link
      to="/orgs/$orgId/notifications"
      params={{ orgId }}
      className={buttonVariants({ variant: 'ghost', size: 'icon', className: 'relative' })}
      aria-label={unread > 0 ? t`Notifications, ${unread} unread` : t`Notifications`}
    >
      <BellIcon />
      {unread > 0 ? (
        <span
          data-testid="unread-badge"
          className="absolute end-0.5 top-0.5 flex min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[10px] leading-4 font-semibold text-destructive-foreground"
        >
          {unread > 99 ? '99+' : unread}
        </span>
      ) : null}
    </Link>
  )
}
