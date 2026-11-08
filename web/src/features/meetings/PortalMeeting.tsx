import { Trans, useLingui } from '@lingui/react/macro'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { toast } from '@/lib/toast'
import { useDecisions, useUnits } from './hooks'
import {
  gApi,
  idemHeader,
  orgUrl,
  unwrap,
  useKeyStore,
  useOrgMutation,
  useOrgQuery,
  type S,
  type SiteCtx,
} from './kit/api'
import { QrCode } from './kit/QrCode'
import { PageHeader, QueryBoundary } from './kit/ui'
import { Callout, DownloadButton } from './kit/widgets'
import { MEETING_STATUS_VARIANT, OUTCOME_VARIANT, useGovLabels } from './labels'
import { decisionNo } from './DecisionsPanel'
import { NonBindingNote } from './PollsPanel'

function MyPolls({ ctx, mine }: { ctx: SiteCtx; mine: S['MyMeeting'] }) {
  const { t } = useLingui()
  const labels = useGovLabels()
  const units = useUnits(ctx.orgId, ctx.siteId)
  const keys = useKeyStore()
  const answer = useOrgMutation(
    async (v: { item: string; unit: string; answer: 'yes' | 'no' | 'abstain' }) => {
      const id = `${v.item}-${v.unit}-${v.answer}`
      const r = await unwrap(
        gApi.PUT('/v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll/answer', {
          params: { path: { org_id: ctx.orgId, agenda_item_id: v.item } },
          headers: idemHeader(keys.get(id)),
          body: { unit_id: v.unit, answer: v.answer },
        }),
      )
      keys.done(id)
      return r
    },
  )
  if (mine.polls.length === 0) return null
  const door = (id: string) => units.data?.find((u) => u.id === id)?.door_number ?? id
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
        <CardTitle>
          <Trans>Polls</Trans>
        </CardTitle>
        <NonBindingNote />
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <p className="text-sm text-muted-foreground">
          <Trans>
            Your answer is not a vote: it tells the manager what owners think. The binding vote is
            taken in the room. One answer per unit; you can change it until the poll closes.
          </Trans>
        </p>
        {mine.polls.map((p) => (
          <div key={p.agenda_item_id} className="flex flex-col gap-2 rounded-md border p-3">
            <span className="font-medium">{p.title}</span>
            {mine.eligible_unit_ids.map((u) => (
              <div key={u} className="flex flex-wrap items-center gap-2">
                <span className="w-20 text-sm text-muted-foreground">{door(u)}</span>
                {(['yes', 'no', 'abstain'] as const).map((a) => (
                  <Button
                    key={a}
                    size="sm"
                    variant="outline"
                    disabled={answer.isPending}
                    onClick={() =>
                      answer.mutate(
                        { item: p.agenda_item_id, unit: u, answer: a },
                        {
                          onSuccess: () => toast.info(t`Your answer was saved.`),
                          onError: () => toast.error(t`Could not save your answer.`),
                        },
                      )
                    }
                  >
                    {labels.pollAnswer(a)}
                  </Button>
                ))}
                {p.answered_units.includes(u) ? (
                  <Badge variant="success">
                    <Trans>Answered</Trans>
                  </Badge>
                ) : null}
              </div>
            ))}
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

/** Resident view of an assembly (portal): my call, my check-in QR, polls, decisions, minutes. */
export function PortalMeeting({ ctx, meeting }: { ctx: SiteCtx; meeting: S['Meeting'] }) {
  const { t } = useLingui()
  const labels = useGovLabels()
  const me = useOrgQuery(['my-meeting', meeting.id], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/meetings/{meeting_id}/me', {
        params: { path: { org_id: ctx.orgId, meeting_id: meeting.id } },
      }),
    ),
  )
  const decisions = useDecisions(ctx.orgId, ctx.siteId, { meeting_id: meeting.id })
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
        actions={
          meeting.call_letter_document_id ? (
            <DownloadButton
              url={orgUrl(ctx.orgId, `/meetings/${meeting.id}/call-letter`)}
              filename={`call-letter-${meeting.id}.pdf`}
            >
              <Trans>My call letter</Trans>
            </DownloadButton>
          ) : null
        }
      />
      <QueryBoundary query={me} skeleton="page">
        {(mine) => (
          <>
            {mine.eligible_unit_ids.length === 0 ? (
              <Callout tone="info">
                <Trans>
                  Only owners attend and answer polls. You can read the decisions below.
                </Trans>
              </Callout>
            ) : null}
            {mine.attendance_qr ? (
              <Card>
                <CardHeader>
                  <CardTitle>
                    <Trans>My check-in code</Trans>
                  </CardTitle>
                </CardHeader>
                <CardContent className="flex flex-col items-center gap-3 sm:flex-row">
                  <QrCode value={mine.attendance_qr} label={t`Check-in QR code`} />
                  <div className="flex flex-col gap-2 text-sm">
                    <p>
                      <Trans>Show this code at the door. It works once per assembly.</Trans>
                    </p>
                    {mine.checked_in ? (
                      <Badge variant="success" className="w-fit">
                        <Trans>You are checked in</Trans>
                      </Badge>
                    ) : null}
                  </div>
                </CardContent>
              </Card>
            ) : null}
            <MyPolls ctx={ctx} mine={mine} />
          </>
        )}
      </QueryBoundary>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Decisions</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <QueryBoundary
            query={decisions}
            isEmpty={(d) => d.length === 0}
            emptyTitle={t`No decisions yet`}
            skeleton="page"
          >
            {(items) => (
              <ul className="flex flex-col gap-2">
                {items.map((d) => (
                  <li key={d.id} className="flex flex-col gap-1 rounded-md border p-3">
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-sm font-semibold" dir="ltr">
                        {decisionNo(d)}
                      </span>
                      <Badge variant={OUTCOME_VARIANT[d.outcome] ?? 'secondary'}>
                        {labels.outcome(d.outcome)}
                      </Badge>
                    </div>
                    <p className="text-sm">{d.text}</p>
                  </li>
                ))}
              </ul>
            )}
          </QueryBoundary>
        </CardContent>
      </Card>
    </div>
  )
}
