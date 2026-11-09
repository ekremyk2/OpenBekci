import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { BellIcon, CheckCheckIcon } from 'lucide-react'
import { draftApi, type Notification } from '@/api/draft'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { SiteDate } from '@/components/data/SiteDate'
import { TabBar } from '@/components/data/TabBar'
import { TableSkeleton } from '@/components/data/Skeletons'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { useSite } from '@/features/dashboard/site'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { LoadMore, must, useCursorQuery } from '@/lib/pagination'
import { toast } from '@/lib/toast'
import { notificationTypeLabel, orderedTypes } from './types'

function notificationTitle(n: Notification): string | undefined {
  const title = (n.payload as { title?: unknown }).title
  return typeof title === 'string' ? title : undefined
}

/** Per-user mutes of non-essential types; sign-in codes and invitations cannot be muted. */
export function MutesPanel({ orgId }: { orgId: string }) {
  const { t, i18n } = useLingui()
  const qc = useQueryClient()
  const query = useQuery({
    queryKey: ['operations.getNotificationMutes', orgId],
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/me/notification-mutes', {
          params: { path: { org_id: orgId } },
        }),
      ),
  })
  const save = useMutation({
    mutationFn: async (muted: string[]) =>
      must(
        await draftApi.PUT('/v1/orgs/{org_id}/me/notification-mutes', {
          params: { path: { org_id: orgId } },
          body: { muted_types: muted },
        }),
      ),
    onSuccess: (data) => qc.setQueryData(['operations.getNotificationMutes', orgId], data),
    onError: () => toast.error(t`Could not save your preferences.`),
  })
  if (query.isPending) return <TableSkeleton rows={4} columns={2} />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const { muted_types, unmutable_types } = query.data
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Mute notifications</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>Muted types stop reaching you. Sign-in codes and invitations always arrive.</Trans>
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ul className="flex flex-col gap-3">
          {orderedTypes([...muted_types, ...unmutable_types]).map((type) => {
            const locked = unmutable_types.includes(type)
            const muted = muted_types.includes(type)
            return (
              <li key={type}>
                <Label className="gap-3 font-normal">
                  <Checkbox
                    checked={muted}
                    disabled={locked || save.isPending}
                    onChange={(e) =>
                      save.mutate(
                        e.target.checked
                          ? [...muted_types, type]
                          : muted_types.filter((m) => m !== type),
                      )
                    }
                  />
                  <span>{notificationTypeLabel(type, i18n)}</span>
                  {locked ? (
                    <span className="text-xs text-muted-foreground">
                      <Trans>Always on</Trans>
                    </span>
                  ) : null}
                </Label>
              </li>
            )
          })}
        </ul>
      </CardContent>
    </Card>
  )
}

/**
 * `link` is a server-supplied in-app path. Browsers read a backslash as a slash, so `/\host` and
 * `//host` both leave the site, and an absolute URL does too: the link is resolved against the
 * current origin and only followed when it stays there, and only as a path.
 */
export function inAppPath(link: string): string | null {
  if (!link.startsWith('/') || link.includes('\\')) return null
  try {
    const url = new URL(link, window.location.origin)
    if (url.origin !== window.location.origin) return null
    return url.pathname + url.search + url.hash
  } catch {
    return null
  }
}

export function NotificationsPage() {
  const { t, i18n } = useLingui()
  const { org, can } = useSession()
  const { site } = useSite()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const orgId = org?.id ?? ''
  const [tab, setTab] = useState<'inbox' | 'preferences'>('inbox')
  const [unreadOnly, setUnreadOnly] = useState(false)
  const readAllKey = useIdempotencyKey()
  const tz = site?.timezone ?? 'UTC'

  const list = useCursorQuery<Notification>(
    ['operations.listNotifications', orgId, unreadOnly],
    async (cursor) =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/me/notifications', {
          params: {
            path: { org_id: orgId },
            query: { unread: unreadOnly ? true : undefined, limit: 30, cursor },
          },
        }),
      ),
    { enabled: can('notifications.read') },
  )

  const refresh = () =>
    Promise.all([
      qc.invalidateQueries({ queryKey: ['operations.listNotifications'] }),
      qc.invalidateQueries({ queryKey: ['operations.getUnreadCount'] }),
    ])
  const markRead = useMutation({
    mutationFn: async (n: Notification) =>
      must(
        await draftApi.POST('/v1/orgs/{org_id}/me/notifications/{notification_id}:read', {
          params: { path: { org_id: orgId, notification_id: n.id } },
          ...withIdempotencyKey(crypto.randomUUID()),
        }),
      ),
    onSuccess: refresh,
  })
  const markAll = useMutation({
    mutationFn: async () =>
      draftApi.POST('/v1/orgs/{org_id}/me/notifications:read-all', {
        params: { path: { org_id: orgId } },
        body: {},
        ...withIdempotencyKey(readAllKey.key),
      }),
    onSuccess: async () => {
      readAllKey.renew()
      await refresh()
    },
    onError: () => toast.error(t`Could not mark everything as read.`),
  })

  if (!can('notifications.read')) {
    return (
      <EmptyState
        icon={BellIcon}
        title={<Trans>You do not have access to notifications</Trans>}
        description={<Trans>Ask your manager if you think you should.</Trans>}
      />
    )
  }

  function open(n: Notification) {
    if (!n.read_at) markRead.mutate(n)
    const path = n.link ? inAppPath(n.link) : null
    if (path) void navigate({ href: path })
  }

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="me-auto text-2xl font-semibold">
          <Trans>Notifications</Trans>
        </h1>
      </div>

      <TabBar
        value={tab}
        onValueChange={setTab}
        label={t`Notification views`}
        tabs={[
          { id: 'inbox', label: t`Inbox` },
          { id: 'preferences', label: t`Preferences` },
        ]}
      >
        {tab === 'preferences' ? (
          <MutesPanel orgId={orgId} />
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-3">
              <Label className="gap-2 font-normal">
                <Checkbox checked={unreadOnly} onChange={(e) => setUnreadOnly(e.target.checked)} />
                <Trans>Unread only</Trans>
              </Label>
              <Button
                variant="outline"
                size="sm"
                className="ms-auto"
                disabled={markAll.isPending}
                onClick={() => markAll.mutate()}
              >
                <CheckCheckIcon aria-hidden="true" />
                <Trans>Mark all as read</Trans>
              </Button>
            </div>
            {list.isPending ? (
              <TableSkeleton rows={4} columns={2} />
            ) : list.isError ? (
              <ErrorState error={list.error} onRetry={() => void list.refetch()} />
            ) : list.items.length === 0 ? (
              <EmptyState
                icon={BellIcon}
                title={<Trans>You are all caught up</Trans>}
                description={<Trans>New notifications will appear here.</Trans>}
              />
            ) : (
              <>
                <ul
                  className="flex flex-col divide-y rounded-lg border"
                  aria-label={t`Notifications`}
                >
                  {list.items.map((n) => (
                    <li key={n.id}>
                      <button
                        type="button"
                        onClick={() => open(n)}
                        className="flex w-full items-start gap-3 p-3 text-start outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                      >
                        <span
                          aria-hidden="true"
                          className={`mt-1.5 size-2 shrink-0 rounded-full ${n.read_at ? 'bg-transparent' : 'bg-primary'}`}
                        />
                        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                          <span className={n.read_at ? 'text-sm' : 'text-sm font-semibold'}>
                            {notificationTitle(n) ?? notificationTypeLabel(n.type, i18n)}
                          </span>
                          <span className="text-xs text-muted-foreground">
                            {notificationTypeLabel(n.type, i18n)} ·{' '}
                            <SiteDate value={n.created_at} timeZone={tz} kind="datetime" />
                          </span>
                        </span>
                        {!n.read_at ? (
                          <span className="sr-only">
                            <Trans>Unread</Trans>
                          </span>
                        ) : null}
                      </button>
                    </li>
                  ))}
                </ul>
                <LoadMore query={list} />
              </>
            )}
          </>
        )}
      </TabBar>
    </div>
  )
}
