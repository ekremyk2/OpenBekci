import { Trans } from '@lingui/react/macro'
import type { Announcement, AnnouncementAudience, Channel } from '@/api/draft'
import { ConfirmDialog } from '@/components/data/ConfirmDialog'
import { DeliveryPreviewPanel, useDeliveryPreview } from './DeliveryPreview'

/**
 * "Publish now" for a saved draft (O5): runs the delivery preview for the saved audience and
 * channels, shows the recipient and SMS counts, and only then lets the manager confirm. The
 * channels shown are the ones sent.
 */
export function PublishConfirmDialog({
  open,
  onOpenChange,
  orgId,
  announcement,
  busy,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  orgId: string
  announcement: Pick<Announcement, 'site_id' | 'audience' | 'channels'>
  busy: boolean
  onConfirm: (channels: Channel[]) => void
}) {
  const channels = announcement.channels as Channel[]
  const preview = useDeliveryPreview({
    orgId,
    siteId: announcement.site_id,
    audience: announcement.audience as AnnouncementAudience,
    channels,
    enabled: open,
  })
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={<Trans>Publish this announcement?</Trans>}
      description={<Trans>Check who will be reached before it goes out.</Trans>}
      confirmLabel={<Trans>Publish</Trans>}
      busy={busy}
      confirmDisabled={!preview.current}
      onConfirm={() => onConfirm(channels)}
    >
      <DeliveryPreviewPanel preview={preview} channels={channels} />
    </ConfirmDialog>
  )
}
