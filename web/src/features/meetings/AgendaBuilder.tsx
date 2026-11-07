import { useState } from 'react'
import { ArrowDownIcon, ArrowUpIcon, PlusIcon, Trash2Icon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { S } from './kit/api'
import { Checkbox, Field, Select, Textarea } from './kit/ui'
import { TOPIC_IDS, useGovLabels } from './labels'

export interface DraftItem {
  key: string
  title: string
  description: string
  will_be_voted: boolean
  topic: string
}

export const emptyDraft = (): DraftItem => ({
  key: crypto.randomUUID(),
  title: '',
  description: '',
  will_be_voted: false,
  topic: '',
})

/** "Budget: majority by number and by share" — what a topic needs, from the site's threshold table. */
export function ThresholdHint({
  topic,
  thresholds,
}: {
  topic: string | null | undefined
  thresholds: S['DecisionThreshold'][] | undefined
}) {
  const labels = useGovLabels()
  if (!topic) return null
  const rule = thresholds?.find((t) => t.topic === topic)
  if (!rule) return null
  return (
    <Badge variant="outline" title={rule.note ?? undefined}>
      {labels.rule(rule.rule)}
    </Badge>
  )
}

/** The fields of one agenda item (shared by the builder and the editor of a saved meeting). */
export function AgendaItemFields({
  value,
  onChange,
  thresholds,
  idPrefix,
}: {
  value: Omit<DraftItem, 'key'>
  onChange: (v: Omit<DraftItem, 'key'>) => void
  thresholds: S['DecisionThreshold'][] | undefined
  idPrefix: string
}) {
  const { t } = useLingui()
  const labels = useGovLabels()
  // topics of the site's table first; the known ones the table does not list after them
  const topics = [...new Set([...(thresholds?.map((x) => x.topic) ?? []), ...TOPIC_IDS])]
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label={t`Title`} htmlFor={`${idPrefix}-title`} className="sm:col-span-2">
        <Input
          id={`${idPrefix}-title`}
          value={value.title}
          onChange={(e) => onChange({ ...value, title: e.target.value })}
        />
      </Field>
      <Field label={t`Description`} htmlFor={`${idPrefix}-desc`} className="sm:col-span-2">
        <Textarea
          id={`${idPrefix}-desc`}
          value={value.description}
          onChange={(e) => onChange({ ...value, description: e.target.value })}
        />
      </Field>
      <Field label={t`Topic`} htmlFor={`${idPrefix}-topic`}>
        <Select
          id={`${idPrefix}-topic`}
          value={value.topic}
          onChange={(e) => onChange({ ...value, topic: e.target.value })}
        >
          <option value="">{t`No topic`}</option>
          {topics.map((x) => (
            <option key={x} value={x}>
              {labels.topic(x)}
            </option>
          ))}
        </Select>
      </Field>
      <div className="flex flex-col justify-end gap-2">
        <Checkbox
          label={t`Will be voted on`}
          checked={value.will_be_voted}
          onChange={(e) => onChange({ ...value, will_be_voted: e.target.checked })}
        />
        {value.topic ? (
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Trans>Needs</Trans>
            <ThresholdHint topic={value.topic} thresholds={thresholds} />
          </div>
        ) : null}
      </div>
    </div>
  )
}

/** Ordered agenda items held in the form state (used before the meeting exists). */
export function AgendaBuilder({
  items,
  onChange,
  thresholds,
}: {
  items: DraftItem[]
  onChange: (items: DraftItem[]) => void
  thresholds: S['DecisionThreshold'][] | undefined
}) {
  const { t } = useLingui()
  const labels = useGovLabels()
  const [draft, setDraft] = useState<Omit<DraftItem, 'key'>>({
    title: '',
    description: '',
    will_be_voted: true,
    topic: '',
  })
  function move(i: number, d: -1 | 1) {
    const next = [...items]
    const j = i + d
    if (j < 0 || j >= next.length) return
    ;[next[i], next[j]] = [next[j]!, next[i]!]
    onChange(next)
  }
  return (
    <div className="flex flex-col gap-3">
      {items.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <Trans>No agenda items yet.</Trans>
        </p>
      ) : (
        <ol className="flex flex-col gap-2" aria-label={t`Agenda`}>
          {items.map((it, i) => (
            <li key={it.key} className="flex items-center gap-2 rounded-md border p-2">
              <span className="w-6 text-center text-sm text-muted-foreground">{i + 1}</span>
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="truncate font-medium">{it.title}</span>
                <span className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                  {it.will_be_voted ? <Trans>Voted</Trans> : <Trans>Not voted</Trans>}
                  {it.topic ? (
                    <>
                      <span>{labels.topic(it.topic)}</span>
                      <ThresholdHint topic={it.topic} thresholds={thresholds} />
                    </>
                  ) : null}
                </span>
              </div>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t`Move up`}
                disabled={i === 0}
                onClick={() => move(i, -1)}
              >
                <ArrowUpIcon />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t`Move down`}
                disabled={i === items.length - 1}
                onClick={() => move(i, 1)}
              >
                <ArrowDownIcon />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t`Remove item`}
                onClick={() => onChange(items.filter((x) => x.key !== it.key))}
              >
                <Trash2Icon />
              </Button>
            </li>
          ))}
        </ol>
      )}
      <div className="flex flex-col gap-3 rounded-md border border-dashed p-3">
        <AgendaItemFields value={draft} onChange={setDraft} thresholds={thresholds} idPrefix="new-item" />
        <div>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            disabled={draft.title.trim() === ''}
            onClick={() => {
              onChange([...items, { ...draft, key: crypto.randomUUID(), title: draft.title.trim() }])
              setDraft({ title: '', description: '', will_be_voted: true, topic: '' })
            }}
          >
            <PlusIcon />
            <Trans>Add item</Trans>
          </Button>
        </div>
      </div>
    </div>
  )
}
