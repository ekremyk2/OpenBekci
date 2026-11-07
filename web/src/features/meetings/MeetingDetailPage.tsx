import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { PlayIcon, SquareIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import { AgendaPanel } from './AgendaPanel'
import { DecisionsPanel } from './DecisionsPanel'
import { useMeeting } from './hooks'
import { gApi, idemHeader, orgUrl, unwrap, useOrgMutation, useOrgQuery, useSiteCtx, type S, type SiteCtx } from './kit/api'
import { PageHeader, QueryBoundary, ReasonDialog, Select } from './kit/ui'
import { Callout, DownloadButton } from './kit/widgets'
import { MEETING_STATUS_VARIANT, useGovLabels } from './labels'
import { CallStep, DeliveryChecklist } from './NoticesPanel'
import { PortalMeeting } from './PortalMeeting'
import { PollsPanel } from './PollsPanel'

function StatusActions({ ctx, meeting }: { ctx: SiteCtx; meeting: S['Meeting'] }) {
  const { t } = useLingui()
  const startKey = useIdempotencyKey()
  const closeKey = useIdempotencyKey()
  const cancelKey = useIdempotencyKey()
  const [cancelling, setCancelling] = useState(false)
  const path = (a: 'start' | 'close') =>
    a === 'start'
      ? ('/v1/orgs/{org_id}/meetings/{meeting_id}:start' as const)
      : ('/v1/orgs/{org_id}/meetings/{meeting_id}:close' as const)
  const move = useOrgMutation(async (a: 'start' | 'close') =>
    unwrap(
      gApi.POST(path(a), {
        params: {
          path: { org_id: ctx.orgId, meeting_id: meeting.id },
          header: idemHeader(a === 'start' ? startKey.key : closeKey.key),
        },
      }),
    ),
  )
  const cancel = useOrgMutation(async (reason: string) =>
    unwrap(
      gApi.POST('/v1/orgs/{org_id}/meetings/{meeting_id}:cancel', {
        params: { path: { org_id: ctx.orgId, meeting_id: meeting.id }, header: idemHeader(cancelKey.key) },
        body: { reason },
      }),
    ),
  )
  const s = meeting.status
  return (
    <>
      {s === 'called' ? (
        <Button onClick={() => move.mutate('start', { onError: () => toast.error(t`Could not start the assembly.`) })} disabled={move.isPending}>
          <PlayIcon />
          <Trans>Start the assembly</Trans>
        </Button>
      ) : null}
      {s === 'in_session' ? (
        <Button
          variant="secondary"
          onClick={() => move.mutate('close', { onError: () => toast.error(t`Could not close the assembly.`) })}
          disabled={move.isPending}
        >
          <SquareIcon />
          <Trans>Close the assembly</Trans>
        </Button>
      ) : null}
      {s === 'called' || s === 'in_session' ? (
        <Link
          to="/orgs/$orgId/meetings/$meetingId/desk"
          params={{ orgId: ctx.orgId, meetingId: meeting.id }}
          className={buttonVariants({ variant: 'outline' })}
        >
          <Trans>Attendance desk</Trans>
        </Link>
      ) : null}
      {s === 'draft' || s === 'called' ? (
        <Button variant="ghost" onClick={() => setCancelling(true)}>
          <Trans>Cancel the assembly</Trans>
        </Button>
      ) : null}
      <ReasonDialog
        open={cancelling}
        onOpenChange={setCancelling}
        title={t`Cancel this assembly?`}
        description={t`Owners who were called are not notified automatically; record the cancellation with a reason.`}
        confirmLabel={t`Cancel the assembly`}
        destructive
        pending={cancel.isPending}
        onConfirm={(reason) => cancel.mutate(reason, { onSuccess: () => setCancelling(false) })}
      />
    </>
  )
}

/** Minutes (G1) and the post-meeting notification of absent owners (G3). */
function MinutesPanel({ ctx, meeting }: { ctx: SiteCtx; meeting: S['Meeting'] }) {
  const { t } = useLingui()
  const genKey = useIdempotencyKey()
  const notifyKey = useIdempotencyKey()
  const [channels, setChannels] = useState('email')
  const [preview, setPreview] = useState<S['DecisionNotifyResult'] | null>(null)
  const closed = meeting.status === 'closed'
  const generate = useOrgMutation(async () =>
    unwrap(
      gApi.POST('/v1/orgs/{org_id}/meetings/{meeting_id}/minutes:generate', {
        params: { path: { org_id: ctx.orgId, meeting_id: meeting.id }, header: idemHeader(genKey.key) },
      }),
    ),
  )
  const notify = useOrgMutation(async (dry: boolean) =>
    unwrap(
      gApi.POST('/v1/orgs/{org_id}/meetings/{meeting_id}:notify-decisions', {
        params: {
          path: { org_id: ctx.orgId, meeting_id: meeting.id },
          query: { dry_run: dry },
          header: idemHeader(dry ? `${notifyKey.key}-preview` : notifyKey.key),
        },
        body: { channels: channels === 'sms' ? ['in_app', 'sms'] : channels === 'email' ? ['in_app', 'email'] : ['in_app'] },
      }),
    ),
  )
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Minutes (tutanak)</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-2">
          {!closed ? (
            <p className="text-sm text-muted-foreground">
              <Trans>The minutes can be generated once the assembly is closed.</Trans>
            </p>
          ) : (
            <>
              <Button
                disabled={generate.isPending}
                onClick={() =>
                  generate.mutate(undefined, {
                    onSuccess: () => toast.info(t`The minutes were generated and filed in the documents.`),
                    onError: () => toast.error(t`Could not generate the minutes.`),
                  })
                }
              >
                {meeting.minutes_document_id ? t`Generate again` : t`Generate the minutes`}
              </Button>
              {meeting.minutes_document_id ? (
                <MinutesDownload ctx={ctx} meetingId={meeting.id} />
              ) : null}
            </>
          )}
        </CardContent>
      </Card>
      {closed ? (
        <Card>
          <CardHeader>
            <CardTitle>
              <Trans>Tell the absent owners</Trans>
            </CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <p className="text-sm text-muted-foreground">
              <Trans>The decisions go to the owners who were not present. Each delivery is recorded like the call.</Trans>
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <Select aria-label={t`Channel`} className="w-auto" value={channels} onChange={(e) => { setChannels(e.target.value); setPreview(null) }}>
                <option value="in_app">{t`In the app only`}</option>
                <option value="email">{t`App and email`}</option>
                <option value="sms">{t`App and SMS`}</option>
              </Select>
              <Button
                variant="outline"
                disabled={notify.isPending}
                onClick={() => notify.mutate(true, { onSuccess: setPreview, onError: () => toast.error(t`Could not check the recipients.`) })}
              >
                <Trans>Check recipients</Trans>
              </Button>
              <Button
                disabled={notify.isPending || !preview}
                onClick={() =>
                  notify.mutate(false, {
                    onSuccess: () => {
                      toast.info(t`The decisions were sent.`)
                      setPreview(null)
                    },
                    onError: () => toast.error(t`Could not send the decisions.`),
                  })
                }
              >
                <Trans>Send the decisions</Trans>
              </Button>
            </div>
            {preview ? (
              <Callout tone="info" role="status">
                <Trans>
                  {preview.recipient_count} owners will be notified ({preview.sms_count} by SMS).
                </Trans>
              </Callout>
            ) : null}
          </CardContent>
        </Card>
      ) : null}
      {closed ? <DeliveryChecklist ctx={ctx} meeting={meeting} phase="decisions" /> : null}
    </div>
  )
}

function MinutesDownload({ ctx, meetingId }: { ctx: SiteCtx; meetingId: string }) {
  const q = useMeetingMinutes(ctx, meetingId)
  if (!q.data) return null
  return (
    <DownloadButton url={orgUrl(ctx.orgId, `/files/${q.data.file_id}`)} filename={`minutes-${meetingId}.pdf`} variant="outline">
      <Trans>Minutes (PDF)</Trans>
    </DownloadButton>
  )
}

function useMeetingMinutes(ctx: SiteCtx, meetingId: string) {
  return useOrgQuery(['minutes', meetingId], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/meetings/{meeting_id}/minutes', {
        params: { path: { org_id: ctx.orgId, meeting_id: meetingId } },
      }),
    ),
  )
}

function ManagerMeeting({ ctx, meeting }: { ctx: SiteCtx; meeting: S['Meeting'] }) {
  const { t } = useLingui()
  const labels = useGovLabels()
  const called = meeting.status !== 'draft' && meeting.status !== 'cancelled'
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={meeting.title ?? labels.meetingKind(meeting.kind)}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <Badge variant={MEETING_STATUS_VARIANT[meeting.status] ?? 'secondary'}>
              {labels.meetingStatus(meeting.status)}
            </Badge>
            <SiteDate value={meeting.scheduled_at} timeZone={ctx.timeZone} kind="datetime" />
            <span>·</span>
            <span>{meeting.location}</span>
          </span>
        }
        actions={<StatusActions ctx={ctx} meeting={meeting} />}
      />
      <p className="text-sm text-muted-foreground">
        <Trans>Second meeting:</Trans>{' '}
        <SiteDate value={meeting.second_call_at} timeZone={ctx.timeZone} kind="datetime" />
      </p>
      <Tabs defaultValue={called ? 'call' : 'agenda'}>
        <TabsList className="h-auto flex-wrap">
          <TabsTrigger value="agenda">{t`Agenda`}</TabsTrigger>
          <TabsTrigger value="call">{t`Call`}</TabsTrigger>
          <TabsTrigger value="polls">{t`Polls`}</TabsTrigger>
          <TabsTrigger value="decisions">{t`Counts and decisions`}</TabsTrigger>
          <TabsTrigger value="minutes">{t`Minutes`}</TabsTrigger>
        </TabsList>
        <TabsContent value="agenda">
          <AgendaPanel ctx={ctx} meeting={meeting} />
        </TabsContent>
        <TabsContent value="call">
          {meeting.status === 'draft' ? (
            <CallStep ctx={ctx} meeting={meeting} />
          ) : called ? (
            <DeliveryChecklist ctx={ctx} meeting={meeting} />
          ) : (
            <p className="text-sm text-muted-foreground">
              <Trans>This assembly was cancelled.</Trans>
            </p>
          )}
        </TabsContent>
        <TabsContent value="polls">
          <PollsPanel ctx={ctx} meetingId={meeting.id} />
        </TabsContent>
        <TabsContent value="decisions">
          <DecisionsPanel ctx={ctx} meeting={meeting} />
        </TabsContent>
        <TabsContent value="minutes">
          <MinutesPanel ctx={ctx} meeting={meeting} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

export function MeetingDetailPage({ meetingId }: { meetingId: string }) {
  const { can } = useSession()
  const ctx = useSiteCtx()!
  const meeting = useMeeting(ctx.orgId, meetingId)
  const manage = can('meetings.manage')
  return (
    <QueryBoundary query={meeting} skeleton="page">
      {(m) => (manage ? <ManagerMeeting ctx={ctx} meeting={m} /> : <PortalMeeting ctx={ctx} meeting={m} />)}
    </QueryBoundary>
  )
}
