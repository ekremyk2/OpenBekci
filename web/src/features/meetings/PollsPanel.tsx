import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { toast } from '@/lib/toast'
import { gApi, ifMatch, unwrap, useOrgMutation, useOrgQuery, type S, type SiteCtx } from './kit/api'
import { instantToLocalInput, localInputToInstant } from './kit/time'
import { Field } from './kit/ui'
import { Callout } from './kit/widgets'
import { useAgenda } from './hooks'
import { useGovLabels } from './labels'

/** The one thing every poll screen must say: answers are not votes. */
export function NonBindingNote() {
  return (
    <Badge variant="info" title="binding: false">
      <Trans>Non-binding</Trans>
    </Badge>
  )
}

function Bar({ label, value, total }: { label: string; value: number; total: number }) {
  const pct = total === 0 ? 0 : Math.round((value / total) * 100)
  return (
    <div className="flex items-center gap-2 text-sm">
      <span className="w-20">{label}</span>
      <div className="h-2 flex-1 overflow-hidden rounded-full bg-muted">
        <div
          className="h-full bg-primary"
          ref={(el) => el?.style.setProperty('width', `${pct}%`)}
        />
      </div>
      <span className="w-8 text-end tabular-nums">{value}</span>
    </div>
  )
}

export function PollResultsView({ results }: { results: S['PollResults'] }) {
  const labels = useGovLabels()
  const total = results.yes + results.no + results.abstain
  return (
    <div className="flex flex-col gap-2" data-testid="poll-results">
      <div className="flex flex-wrap items-center gap-2">
        {results.binding ? (
          <Badge variant="warning">
            <Trans>Binding</Trans>
          </Badge>
        ) : (
          <NonBindingNote />
        )}
        <span className="text-sm text-muted-foreground">
          <Trans>
            {results.units_answered} of {results.units_total} units answered
          </Trans>
        </span>
      </div>
      <Bar label={labels.pollAnswer('yes')} value={results.yes} total={total} />
      <Bar label={labels.pollAnswer('no')} value={results.no} total={total} />
      <Bar label={labels.pollAnswer('abstain')} value={results.abstain} total={total} />
      <p className="text-xs text-muted-foreground">
        <Trans>The binding vote happens in the room; this is only a temperature check.</Trans>
      </p>
    </div>
  )
}

function PollCard({ ctx, item }: { ctx: SiteCtx; item: S['AgendaItem'] }) {
  const { t } = useLingui()
  const poll = useOrgQuery(['poll', item.id], async () => {
    try {
      return await unwrap(
        gApi.GET('/v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll', {
          params: { path: { org_id: ctx.orgId, agenda_item_id: item.id } },
        }),
      )
    } catch (e) {
      if (e instanceof AppError && e.status === 404) return null
      throw e
    }
  })
  const results = useOrgQuery(
    ['poll-results', item.id],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll/results', {
          params: { path: { org_id: ctx.orgId, agenda_item_id: item.id } },
        }),
      ),
    { enabled: poll.data != null, refetchInterval: poll.data?.status === 'open' ? 10_000 : false },
  )
  const [opens, setOpens] = useState('')
  const [closes, setCloses] = useState('')
  const [question, setQuestion] = useState('')
  const save = useOrgMutation(async (v: { opens_at: string; closes_at: string }) =>
    unwrap(
      gApi.PUT('/v1/orgs/{org_id}/agenda-items/{agenda_item_id}/poll', {
        params: {
          path: { org_id: ctx.orgId, agenda_item_id: item.id },
          header: ifMatch(poll.data?.version ?? 0),
        },
        body: { ...v, ...(question ? { question } : {}) },
      }),
    ),
  )
  const p = poll.data
  const statusLabel =
    p?.status === 'open'
      ? t`Open`
      : p?.status === 'closed'
        ? t`Closed`
        : p
          ? t`Scheduled`
          : t`No poll`
  const now = new Date()
  const inDays = (d: number) => new Date(now.getTime() + d * 86_400_000).toISOString()
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
        <CardTitle className="text-base">{item.title}</CardTitle>
        <div className="flex items-center gap-2">
          <NonBindingNote />
          <Badge variant={p?.status === 'open' ? 'success' : 'secondary'}>{statusLabel}</Badge>
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label={t`Opens`} htmlFor={`po-${item.id}`}>
            <Input
              id={`po-${item.id}`}
              type="datetime-local"
              dir="ltr"
              value={opens || (p ? instantToLocalInput(p.opens_at, ctx.timeZone) : '')}
              onChange={(e) => setOpens(e.target.value)}
            />
          </Field>
          <Field label={t`Closes`} htmlFor={`pc-${item.id}`}>
            <Input
              id={`pc-${item.id}`}
              type="datetime-local"
              dir="ltr"
              value={closes || (p ? instantToLocalInput(p.closes_at, ctx.timeZone) : '')}
              onChange={(e) => setCloses(e.target.value)}
            />
          </Field>
          <Field label={t`Question (optional)`} htmlFor={`pq-${item.id}`}>
            <Input
              id={`pq-${item.id}`}
              value={question || (p?.question ?? '')}
              onChange={(e) => setQuestion(e.target.value)}
            />
          </Field>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            disabled={save.isPending}
            onClick={() =>
              save.mutate(
                {
                  opens_at: now.toISOString(),
                  closes_at:
                    p?.closes_at && Date.parse(p.closes_at) > Date.now() ? p.closes_at : inDays(7),
                },
                {
                  onSuccess: () => toast.info(t`The poll is open.`),
                  onError: () => toast.error(t`Could not save the poll.`),
                },
              )
            }
          >
            <Trans>Open now</Trans>
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={save.isPending || !p || p.status === 'closed'}
            onClick={() =>
              save.mutate(
                { opens_at: p!.opens_at, closes_at: now.toISOString() },
                {
                  onSuccess: () => toast.info(t`The poll is closed.`),
                  onError: () => toast.error(t`Could not save the poll.`),
                },
              )
            }
          >
            <Trans>Close now</Trans>
          </Button>
          <Button
            size="sm"
            variant="secondary"
            disabled={save.isPending || (!opens && !p) || (!closes && !p)}
            onClick={() =>
              save.mutate(
                {
                  opens_at: localInputToInstant(
                    opens || instantToLocalInput(p!.opens_at, ctx.timeZone),
                    ctx.timeZone,
                  ),
                  closes_at: localInputToInstant(
                    closes || instantToLocalInput(p!.closes_at, ctx.timeZone),
                    ctx.timeZone,
                  ),
                },
                {
                  onSuccess: () => toast.info(t`Poll saved.`),
                  onError: () => toast.error(t`Could not save the poll.`),
                },
              )
            }
          >
            <Trans>Save dates</Trans>
          </Button>
        </div>
        {results.data ? <PollResultsView results={results.data} /> : null}
      </CardContent>
    </Card>
  )
}

/** Manager view (G2): open and close non-binding polls per voted agenda item and read the temperature. */
export function PollsPanel({ ctx, meetingId }: { ctx: SiteCtx; meetingId: string }) {
  const { t } = useLingui()
  const agenda = useAgenda(ctx.orgId, meetingId)
  const items = (agenda.data ?? []).filter((a) => a.will_be_voted)
  return (
    <div className="flex flex-col gap-4">
      <Callout tone="info">
        <Trans>
          Polls are not votes. Owners answer once per unit before the assembly so you can see the
          mood; the binding count is recorded in the room.
        </Trans>
      </Callout>
      {items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t`No agenda item is marked "will be voted".`}</p>
      ) : (
        items.map((a) => <PollCard key={a.id} ctx={ctx} item={a} />)
      )}
    </div>
  )
}
