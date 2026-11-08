import { useQuery } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { LoaderCircleIcon } from 'lucide-react'
import { apiDraft, type AnnouncementAudience, type Channel } from '@/api/draft'
import { must } from '@/lib/pagination'

/**
 * Recipient and SMS counts for an audience and channel set (O5). `:preview` is a read and takes no
 * `Idempotency-Key`: the audience changes between calls, and the server rejects one key reused
 * with a different body (422 `idempotency_mismatch`).
 */
export function useDeliveryPreview({
  orgId,
  siteId,
  audience,
  channels,
  enabled = true,
}: {
  orgId: string
  siteId: string | undefined
  audience: AnnouncementAudience
  channels: readonly Channel[]
  enabled?: boolean
}) {
  const ready =
    !!siteId &&
    channels.length > 0 &&
    (audience.kind !== 'units' || (audience.unit_ids?.length ?? 0) > 0) &&
    (audience.kind !== 'block' || !!audience.block_id)
  const query = useQuery({
    queryKey: [
      'operations.previewAnnouncement',
      orgId,
      siteId,
      audience,
      [...channels].sort((a, b) => a.localeCompare(b)),
    ],
    enabled: ready && enabled,
    staleTime: 0,
    queryFn: async () =>
      must(
        await apiDraft.POST('/v1/orgs/{org_id}/announcements:preview', {
          params: { path: { org_id: orgId } },
          body: { site_id: siteId!, audience, channels: [...channels] },
        }),
      ),
  })
  /** True only when the counts on screen belong to the audience and channels now chosen. */
  const current = ready && query.isSuccess && !query.isRefetchError
  return { query, ready, current }
}

export type DeliveryPreviewState = ReturnType<typeof useDeliveryPreview>

/** Presentational counts panel; the caller owns the query so it can gate publishing on it. */
export function DeliveryPreviewPanel({
  preview,
  channels,
}: {
  preview: DeliveryPreviewState
  channels: readonly Channel[]
}) {
  const { t } = useLingui()
  const { query, ready } = preview
  return (
    <div
      role="status"
      aria-live="polite"
      aria-label={t`Delivery preview`}
      className="rounded-md border bg-muted/40 p-3 text-sm"
    >
      {!ready ? (
        <p className="text-muted-foreground">
          <Trans>Choose an audience and at least one channel to see who will be reached.</Trans>
        </p>
      ) : query.isPending ? (
        <p className="flex items-center gap-2 text-muted-foreground">
          <LoaderCircleIcon className="size-4 animate-spin" aria-hidden="true" />
          <Trans>Counting recipients</Trans>
        </p>
      ) : query.isError ? (
        <p className="text-destructive">
          <Trans>Could not count the recipients.</Trans>
        </p>
      ) : (
        <ul className="flex flex-col gap-1">
          <li>
            <Trans>Recipients: {query.data.recipient_count}</Trans>
          </li>
          {channels.includes('in_app') ? (
            <li>
              <Trans>In-app notifications: {query.data.in_app_count}</Trans>
            </li>
          ) : null}
          {channels.includes('email') ? (
            <li>
              <Trans>Emails: {query.data.email_count}</Trans>
            </li>
          ) : null}
          {channels.includes('sms') ? (
            <li className="font-medium">
              <Trans>SMS messages: {query.data.sms_count} (billed to the site)</Trans>
            </li>
          ) : null}
        </ul>
      )}
    </div>
  )
}
