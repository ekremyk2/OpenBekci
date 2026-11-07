import { useState } from 'react'
import { ArrowDownIcon, ArrowUpIcon, PencilIcon, PlusIcon, Trash2Icon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { toast } from '@/lib/toast'
import { AgendaItemFields, ThresholdHint, type DraftItem } from './AgendaBuilder'
import { useAgenda, useGovernanceSettings } from './hooks'
import { gApi, idemHeader, ifMatch, useKeyStore, useOrgMutation, type S, type SiteCtx } from './kit/api'
import { QueryBoundary } from './kit/ui'
import { Callout } from './kit/widgets'
import { useGovLabels } from './labels'

type Fields = Omit<DraftItem, 'key'>
const toFields = (a: S['AgendaItem']): Fields => ({
  title: a.title,
  description: a.description ?? '',
  will_be_voted: a.will_be_voted,
  topic: a.topic ?? '',
})

/** The agenda of a saved assembly: editable in draft, read-only once the call has gone out. */
export function AgendaPanel({ ctx, meeting }: { ctx: SiteCtx; meeting: S['Meeting'] }) {
  const { t } = useLingui()
  const labels = useGovLabels()
  const agenda = useAgenda(ctx.orgId, meeting.id)
  const settings = useGovernanceSettings(ctx.orgId, ctx.siteId)
  const thresholds = settings.data?.settings.thresholds
  const editable = meeting.status === 'draft'
  const keys = useKeyStore()
  const [editing, setEditing] = useState<string | null>(null)
  const [fields, setFields] = useState<Fields>({ title: '', description: '', will_be_voted: true, topic: '' })
  const [adding, setAdding] = useState(false)

  const payload = (f: Fields) => ({
    title: f.title.trim(),
    description: f.description.trim() === '' ? null : f.description.trim(),
    will_be_voted: f.will_be_voted,
    topic: f.topic === '' ? null : f.topic,
  })
  const add = useOrgMutation(async (f: Fields) => {
    await gApi.POST('/v1/orgs/{org_id}/meetings/{meeting_id}/agenda-items', {
      params: { path: { org_id: ctx.orgId, meeting_id: meeting.id }, header: idemHeader(keys.get(`add-${f.title}`)) },
      body: payload(f),
    })
    keys.done(`add-${f.title}`)
  })
  const update = useOrgMutation(async (v: { item: S['AgendaItem']; f: Fields }) => {
    await gApi.PATCH('/v1/orgs/{org_id}/agenda-items/{agenda_item_id}', {
      params: {
        path: { org_id: ctx.orgId, agenda_item_id: v.item.id },
        header: ifMatch(v.item.version),
      },
      body: payload(v.f),
    })
  })
  const remove = useOrgMutation(async (item: S['AgendaItem']) => {
    await gApi.DELETE('/v1/orgs/{org_id}/agenda-items/{agenda_item_id}', {
      params: { path: { org_id: ctx.orgId, agenda_item_id: item.id }, header: ifMatch(item.version) },
    })
  })
  const reorder = useOrgMutation(async (ids: string[]) => {
    await gApi.PUT('/v1/orgs/{org_id}/meetings/{meeting_id}/agenda-items:order', {
      params: { path: { org_id: ctx.orgId, meeting_id: meeting.id } },
      body: { item_ids: ids },
    })
  })
  const fail = () => toast.error(t`Could not save the agenda.`)

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Agenda</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {!editable ? (
          <Callout tone="info" role="status">
            <Trans>The call has gone out, so the agenda is fixed.</Trans>
          </Callout>
        ) : null}
        <QueryBoundary
          query={agenda}
          isEmpty={(d) => d.length === 0}
          emptyTitle={t`No agenda items yet`}
          emptyDescription={editable ? t`Add the items to discuss and vote on.` : undefined}
        >
          {(items) => (
            <ol className="flex flex-col gap-2" aria-label={t`Agenda`}>
              {items.map((a, i) => (
                <li key={a.id} className="flex flex-col gap-2 rounded-md border p-3">
                  {editing === a.id ? (
                    <>
                      <AgendaItemFields value={fields} onChange={setFields} thresholds={thresholds} idPrefix={`ed-${a.id}`} />
                      <div className="flex gap-2">
                        <Button
                          size="sm"
                          disabled={fields.title.trim() === '' || update.isPending}
                          onClick={() =>
                            update.mutate({ item: a, f: fields }, { onSuccess: () => setEditing(null), onError: fail })
                          }
                        >
                          <Trans>Save</Trans>
                        </Button>
                        <Button size="sm" variant="outline" onClick={() => setEditing(null)}>
                          <Trans>Cancel</Trans>
                        </Button>
                      </div>
                    </>
                  ) : (
                    <div className="flex items-start gap-3">
                      <span className="w-6 pt-0.5 text-center text-sm text-muted-foreground">{i + 1}</span>
                      <div className="flex min-w-0 flex-1 flex-col gap-1">
                        <span className="font-medium">{a.title}</span>
                        {a.description ? <span className="text-sm text-muted-foreground">{a.description}</span> : null}
                        <span className="flex flex-wrap items-center gap-2 text-xs">
                          <Badge variant={a.will_be_voted ? 'info' : 'secondary'}>
                            {a.will_be_voted ? t`Will be voted` : t`For discussion`}
                          </Badge>
                          {a.topic ? (
                            <>
                              <span className="text-muted-foreground">{labels.topic(a.topic)}</span>
                              <ThresholdHint topic={a.topic} thresholds={thresholds} />
                            </>
                          ) : null}
                        </span>
                      </div>
                      {editable ? (
                        <div className="flex gap-1">
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={t`Move up`}
                            disabled={i === 0 || reorder.isPending}
                            onClick={() => {
                              const ids = items.map((x) => x.id)
                              ;[ids[i - 1], ids[i]] = [ids[i]!, ids[i - 1]!]
                              reorder.mutate(ids, { onError: fail })
                            }}
                          >
                            <ArrowUpIcon />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={t`Move down`}
                            disabled={i === items.length - 1 || reorder.isPending}
                            onClick={() => {
                              const ids = items.map((x) => x.id)
                              ;[ids[i + 1], ids[i]] = [ids[i]!, ids[i + 1]!]
                              reorder.mutate(ids, { onError: fail })
                            }}
                          >
                            <ArrowDownIcon />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={t`Edit item`}
                            onClick={() => {
                              setFields(toFields(a))
                              setEditing(a.id)
                            }}
                          >
                            <PencilIcon />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={t`Remove item`}
                            onClick={() => remove.mutate(a, { onError: fail })}
                          >
                            <Trash2Icon />
                          </Button>
                        </div>
                      ) : null}
                    </div>
                  )}
                </li>
              ))}
            </ol>
          )}
        </QueryBoundary>
        {editable ? (
          adding ? (
            <div className="flex flex-col gap-3 rounded-md border border-dashed p-3">
              <AgendaItemFields value={fields} onChange={setFields} thresholds={thresholds} idPrefix="add-item" />
              <div className="flex gap-2">
                <Button
                  size="sm"
                  disabled={fields.title.trim() === '' || add.isPending}
                  onClick={() =>
                    add.mutate(fields, {
                      onSuccess: () => {
                        setAdding(false)
                        setFields({ title: '', description: '', will_be_voted: true, topic: '' })
                      },
                      onError: fail,
                    })
                  }
                >
                  <Trans>Add item</Trans>
                </Button>
                <Button size="sm" variant="outline" onClick={() => setAdding(false)}>
                  <Trans>Cancel</Trans>
                </Button>
              </div>
            </div>
          ) : (
            <div>
              <Button
                size="sm"
                variant="secondary"
                onClick={() => {
                  setFields({ title: '', description: '', will_be_voted: true, topic: '' })
                  setAdding(true)
                }}
              >
                <PlusIcon />
                <Trans>Add an item</Trans>
              </Button>
            </div>
          )
        ) : null}
      </CardContent>
    </Card>
  )
}
