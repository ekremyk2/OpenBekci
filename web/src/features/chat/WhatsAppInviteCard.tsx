import { useQuery } from '@tanstack/react-query'
import { Trans } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { useSession } from '@/app/session'
import { Card, CardContent } from '@/components/ui/card'
import { useSite } from '@/features/dashboard/site'
import { must } from '@/lib/pagination'

/** Resident Home: link to the site's WhatsApp group, hidden when none is set or the read fails. */
export function WhatsAppInviteCard() {
  const { org, can } = useSession()
  const { site } = useSite()
  const orgId = org?.id ?? ''
  const info = useQuery({
    queryKey: ['operations.getSiteChatInfo', orgId, site?.id],
    enabled: !!orgId && !!site && can('notifications.read'),
    retry: false,
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/sites/{site_id}/chat-info', {
          params: { path: { org_id: orgId, site_id: site!.id } },
        }),
      ),
  })
  const url = info.data?.whatsapp_invite_url
  if (!url) return null
  return (
    <Card>
      <CardContent className="pt-6">
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          className="text-primary underline-offset-4 hover:underline"
        >
          <Trans>Join the site's WhatsApp group</Trans>
        </a>
      </CardContent>
    </Card>
  )
}
