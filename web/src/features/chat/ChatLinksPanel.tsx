import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { AppError } from '@/api/errors'
import { useSession } from '@/app/session'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { QrCode } from '@/features/meetings/kit/QrCode'
import { must } from '@/lib/pagination'

const providerName = (p: string) =>
  p === 'telegram' ? 'Telegram' : p === 'whatsapp' ? 'WhatsApp' : p

/**
 * The caller's linked chat accounts (Telegram, WhatsApp): link a new one through a deep link and
 * QR code, unlink an old one. Shown on `/me` to everyone who reads notifications.
 */
export function ChatLinksPanel() {
  const { org, can } = useSession()
  const { t } = useLingui()
  const orgId = org?.id ?? ''
  const qc = useQueryClient()
  const key = ['operations.listChatLinks', orgId]
  const enabled = !!orgId && can('notifications.read')
  const links = useQuery({
    queryKey: key,
    enabled,
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/me/chat-links', {
          params: { path: { org_id: orgId } },
        }),
      ).items,
  })
  const start = useMutation({
    mutationFn: async (provider: string) => ({
      ...must(
        await draftApi.POST('/v1/orgs/{org_id}/me/chat-links/{provider}:start', {
          params: { path: { org_id: orgId, provider } },
        }),
      ),
    }),
    onSuccess: () => qc.invalidateQueries({ queryKey: key }),
  })
  const unlink = useMutation({
    mutationFn: async (linkId: string) =>
      await draftApi.DELETE('/v1/orgs/{org_id}/me/chat-links/{link_id}', {
        params: { path: { org_id: orgId, link_id: linkId } },
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: key }),
  })
  if (!enabled) return null
  const startedName = providerName(start.data?.provider ?? '')
  const notSetUp = start.error instanceof AppError && start.error.status === 409

  const statusBadge = (status: string) =>
    status === 'linked' ? (
      <Badge variant="success">
        <Trans>Linked</Trans>
      </Badge>
    ) : status === 'pending' ? (
      <Badge variant="warning">
        <Trans>Waiting for your message to the bot</Trans>
      </Badge>
    ) : (
      <Badge variant="destructive">
        <Trans>The chat could not be reached; link again</Trans>
      </Badge>
    )

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Linked chats</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>Receive notifications in Telegram or WhatsApp.</Trans>
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {links.data && links.data.length > 0 ? (
          <ul className="flex flex-col gap-2">
            {links.data.map((l) => {
              const name = providerName(l.provider)
              return (
                <li key={l.id} className="flex flex-wrap items-center gap-2 text-sm">
                  <span className="font-medium">{name}</span>
                  {statusBadge(l.status)}
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={unlink.isPending}
                    aria-label={t`Unlink ${name}`}
                    onClick={() => unlink.mutate(l.id)}
                  >
                    <Trans>Unlink</Trans>
                  </Button>
                </li>
              )
            })}
          </ul>
        ) : links.isSuccess ? (
          <p className="text-sm text-muted-foreground">
            <Trans>No chat is linked yet.</Trans>
          </p>
        ) : null}
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            disabled={start.isPending}
            onClick={() => start.mutate('telegram')}
          >
            <Trans>Link Telegram</Trans>
          </Button>
          <Button
            variant="outline"
            disabled={start.isPending}
            onClick={() => start.mutate('whatsapp')}
          >
            <Trans>Link WhatsApp</Trans>
          </Button>
        </div>
        {notSetUp ? (
          <p role="alert" className="text-sm text-destructive">
            <Trans>This chat service is not set up by your administrator yet.</Trans>
          </p>
        ) : start.isError || unlink.isError ? (
          <p role="alert" className="text-sm text-destructive">
            <Trans>Something went wrong. Please try again.</Trans>
          </p>
        ) : null}
        {start.data ? (
          <div className="flex flex-col items-start gap-2">
            <p className="text-sm">
              <Trans>
                Open this link on the phone that has {startedName}, or scan the code, then send the
                message the bot offers.
              </Trans>
            </p>
            <a
              href={start.data.deep_link}
              target="_blank"
              rel="noopener noreferrer"
              className="break-all text-sm text-primary underline-offset-4 hover:underline"
              dir="ltr"
            >
              {start.data.deep_link}
            </a>
            <QrCode value={start.data.deep_link} label={t`Chat link QR code`} size={176} />
          </div>
        ) : null}
      </CardContent>
    </Card>
  )
}
