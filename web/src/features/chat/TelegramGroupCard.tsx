import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { AppError } from '@/api/errors'
import { useSession } from '@/app/session'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { must } from '@/lib/pagination'
import { toast } from '@/lib/toast'

/** The site's Telegram group and WhatsApp invite link (`chatgroups.manage`). */
export function useSiteChatGroup(orgId: string, siteId: string, enabled = true) {
  return useQuery({
    queryKey: ['operations.getSiteChatGroup', orgId, siteId],
    enabled: enabled && !!orgId && !!siteId,
    // pending: the bot may link the group any moment
    refetchInterval: (q) => (q.state.data?.status === 'pending' ? 4000 : false),
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/sites/{site_id}/chat-group', {
          params: { path: { org_id: orgId, site_id: siteId } },
        }),
      ),
  })
}

export function SiteChatGroupCards({ siteId, siteName }: { siteId: string; siteName: string }) {
  const { org, can } = useSession()
  const { t } = useLingui()
  const orgId = org?.id ?? ''
  const qc = useQueryClient()
  const key = ['operations.getSiteChatGroup', orgId, siteId]
  const group = useSiteChatGroup(orgId, siteId, can('chatgroups.manage'))
  const [started, setStarted] = useState<{ code: string; bot: string | null } | null>(null)
  const [url, setUrl] = useState<string | null>(null)
  const path = { org_id: orgId, site_id: siteId }
  const start = useMutation({
    mutationFn: async () =>
      must(
        await draftApi.POST('/v1/orgs/{org_id}/sites/{site_id}/chat-group:start', {
          params: { path },
        }),
      ),
    onSuccess: async (r) => {
      setStarted({ code: r.code, bot: r.bot_username })
      await qc.invalidateQueries({ queryKey: key })
    },
  })
  const unlink = useMutation({
    mutationFn: async () =>
      must(
        await draftApi.DELETE('/v1/orgs/{org_id}/sites/{site_id}/chat-group', {
          params: { path },
        }),
      ),
    onSuccess: async () => {
      setStarted(null)
      await qc.invalidateQueries({ queryKey: key })
    },
  })
  const save = useMutation({
    mutationFn: async (v: string | null) =>
      must(
        await draftApi.PATCH('/v1/orgs/{org_id}/sites/{site_id}/chat-group', {
          params: { path },
          body: { whatsapp_invite_url: v },
        }),
      ),
    onSuccess: async () => {
      setUrl(null)
      await qc.invalidateQueries({ queryKey: key })
      await qc.invalidateQueries({ queryKey: ['operations.getSiteChatInfo', orgId, siteId] })
      toast.info(t`Settings saved.`)
    },
  })
  if (!can('chatgroups.manage') || !group.data) return null
  const g = group.data
  const notConfigured = start.error instanceof AppError && start.error.status === 409
  const invalidUrl = save.error instanceof AppError && save.error.isValidation
  const bot = started?.bot ?? g.bot_username
  const shownUrl = url ?? g.whatsapp_invite_url ?? ''
  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className="flex flex-wrap items-center gap-2">
            <span>
              <Trans>Telegram group</Trans> · {siteName}
            </span>
            {g.status === 'linked' ? (
              <Badge variant="success">
                {g.kind === 'channel' ? (
                  <Trans>Linked to channel “{g.title}”</Trans>
                ) : (
                  <Trans>Linked to group “{g.title}”</Trans>
                )}
              </Badge>
            ) : g.status === 'pending' ? (
              <Badge variant="warning">
                <Trans>Waiting for /link</Trans>
              </Badge>
            ) : (
              <Badge variant="secondary">
                <Trans>Not linked</Trans>
              </Badge>
            )}
          </CardTitle>
          <CardDescription>
            <Trans>Announcements and meeting calls can be posted to the site's group.</Trans>
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {g.status === 'pending' && started ? (
            <div className="flex flex-col gap-2 text-sm">
              <p>
                <Trans>1. Add @{bot} to your group as an administrator.</Trans>
              </p>
              <p>
                <Trans>2. Send this message in the group. The code works for 10 minutes.</Trans>
              </p>
              <div className="flex items-center gap-2">
                <code className="rounded bg-muted px-2 py-1" dir="ltr">
                  /link {started.code}
                </code>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void navigator.clipboard?.writeText(`/link ${started.code}`)}
                >
                  <Trans>Copy</Trans>
                </Button>
              </div>
            </div>
          ) : null}
          <div className="flex flex-wrap gap-2">
            {g.status !== 'linked' ? (
              <Button disabled={start.isPending} onClick={() => start.mutate()}>
                {g.status === 'pending' ? (
                  <Trans>New link code</Trans>
                ) : (
                  <Trans>Get link code</Trans>
                )}
              </Button>
            ) : null}
            {g.status !== 'none' ? (
              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <Button variant="outline">
                    <Trans>Unlink</Trans>
                  </Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>
                      <Trans>Unlink the Telegram group?</Trans>
                    </AlertDialogTitle>
                    <AlertDialogDescription>
                      <Trans>The bot leaves the group and nothing is posted there any more.</Trans>
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>
                      <Trans>Cancel</Trans>
                    </AlertDialogCancel>
                    <AlertDialogAction onClick={() => unlink.mutate()}>
                      <Trans>Unlink</Trans>
                    </AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            ) : null}
          </div>
          {notConfigured ? (
            <p role="alert" className="text-sm text-destructive">
              <Trans>Telegram is not configured for this organization</Trans>
              {can('chat.manage') ? (
                <>
                  {' '}
                  <Link to="/orgs/$orgId/settings/chat" params={{ orgId }} className="underline">
                    <Trans>Chat settings</Trans>
                  </Link>
                </>
              ) : null}
            </p>
          ) : start.isError || unlink.isError ? (
            <p role="alert" className="text-sm text-destructive">
              <Trans>Something went wrong. Please try again.</Trans>
            </p>
          ) : null}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>WhatsApp group invite link</Trans> · {siteName}
          </CardTitle>
          <CardDescription>
            <Trans>Residents see this link on their Home screen.</Trans>
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          <Input
            aria-label={t`WhatsApp group invite link`}
            dir="ltr"
            placeholder="https://chat.whatsapp.com/…"
            value={shownUrl}
            aria-invalid={invalidUrl}
            onChange={(e) => setUrl(e.target.value)}
          />
          {invalidUrl ? (
            <p role="alert" className="text-sm text-destructive">
              <Trans>Must look like https://chat.whatsapp.com/…</Trans>
            </p>
          ) : null}
          <div className="flex gap-2">
            <Button
              disabled={save.isPending || !shownUrl.trim()}
              onClick={() => save.mutate(shownUrl.trim())}
            >
              <Trans>Save</Trans>
            </Button>
            <Button
              variant="outline"
              disabled={save.isPending || !g.whatsapp_invite_url}
              onClick={() => save.mutate(null)}
            >
              <Trans>Clear</Trans>
            </Button>
          </div>
        </CardContent>
      </Card>
    </>
  )
}
