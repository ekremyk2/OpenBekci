import { useEffect, useMemo, useState } from 'react'
import { useForm, useFieldArray } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { PlusIcon, Trash2Icon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useSession } from '@/app/session'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import { ApplyDecisionDialog } from './ApplyDecisionDialog'
import { useAgenda, useDecisions, useGovernanceSettings, useOwnerRows, usePersons } from './hooks'
import { gApi, idemHeader, unwrap, useOrgMutation, useOrgQuery, type S, type SiteCtx } from './kit/api'
import { applyServerErrors, Checkbox, Field, FormRootError, Select, Textarea } from './kit/ui'
import { Callout } from './kit/widgets'
import { OUTCOME_VARIANT, useGovLabels } from './labels'
import { effectKindForTopic, evaluateThreshold } from './logic'

export const decisionNo = (d: Pick<S['Decision'], 'year' | 'sequence_no'>) => `${d.year}/${d.sequence_no}`

// ------------------------------------------------------------------ in-room counts

const numStr = z.string().regex(/^\d*$/)
const pctStr = z.string().regex(/^\d*([.,]\d+)?$/)
const toNum = (s: string) => Number(s.replace(',', '.') || 0)

/** The in-room count of one agenda item with the threshold evaluation next to it. */
export function VoteCountsCard({ ctx, item, onRecordDecision, eligibleUnits }: {
  ctx: SiteCtx
  item: S['AgendaItem']
  eligibleUnits: number
  onRecordDecision: (item: S['AgendaItem'], hint: S['VoteCount']['outcome_hint']) => void
}) {
  const { t } = useLingui()
  const labels = useGovLabels()
  const settings = useGovernanceSettings(ctx.orgId, ctx.siteId)
  const counts = useOrgQuery(['vote-counts', item.id], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/agenda-items/{agenda_item_id}/vote-counts', {
        params: { path: { org_id: ctx.orgId, agenda_item_id: item.id } },
      }),
    ),
  )
  const schema = useMemo(
    () =>
      z.object({
        yes: numStr,
        no: numStr,
        abstain: numStr,
        yesShare: pctStr,
      }),
    [],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { yes: '', no: '', abstain: '', yesShare: '' },
  })
  const saved = counts.data
  useEffect(() => {
    if (saved && saved.recorded_at) {
      form.reset({
        yes: String(saved.yes_count),
        no: String(saved.no_count),
        abstain: String(saved.abstain_count),
        yesShare: saved.yes_share === null ? '' : String(saved.yes_share),
      })
    }
    // only when the stored record changes
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [saved?.recorded_at])
  const w = form.watch()
  const yes = toNum(w.yes)
  const rule = settings.data?.settings.thresholds.find((x) => x.topic === item.topic)?.rule
  const evaln = evaluateThreshold(
    rule,
    { yes, no: toNum(w.no), abstain: toNum(w.abstain), yesSharePct: w.yesShare === '' ? null : toNum(w.yesShare) },
    eligibleUnits,
  )
  const save = useOrgMutation(async (v: Values) =>
    unwrap(
      gApi.PUT('/v1/orgs/{org_id}/agenda-items/{agenda_item_id}/vote-counts', {
        params: { path: { org_id: ctx.orgId, agenda_item_id: item.id } },
        body: {
          yes_count: toNum(v.yes),
          no_count: toNum(v.no),
          abstain_count: toNum(v.abstain),
          ...(v.yesShare !== '' ? { yes_share: toNum(v.yesShare) } : {}),
        },
      }),
    ),
  )
  const hint = saved?.outcome_hint ?? 'undetermined'
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
        <CardTitle className="text-base">{item.title}</CardTitle>
        {rule ? <Badge variant="outline">{labels.rule(rule)}</Badge> : null}
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <form
          className="grid gap-3 sm:grid-cols-4"
          noValidate
          onSubmit={form.handleSubmit(async (v) => {
            try {
              await save.mutateAsync(v)
              toast.info(t`Counts saved.`)
            } catch (e) {
              applyServerErrors(form, e, t`Could not save the counts.`)
            }
          })}
        >
          <Field label={t`Yes`} htmlFor={`y-${item.id}`}>
            <Input id={`y-${item.id}`} inputMode="numeric" dir="ltr" {...form.register('yes')} />
          </Field>
          <Field label={t`No`} htmlFor={`n-${item.id}`}>
            <Input id={`n-${item.id}`} inputMode="numeric" dir="ltr" {...form.register('no')} />
          </Field>
          <Field label={t`Abstain`} htmlFor={`a-${item.id}`}>
            <Input id={`a-${item.id}`} inputMode="numeric" dir="ltr" {...form.register('abstain')} />
          </Field>
          <Field label={t`Yes share of land (%)`} htmlFor={`s-${item.id}`}>
            <Input id={`s-${item.id}`} inputMode="decimal" dir="ltr" {...form.register('yesShare')} />
          </Field>
          <div className="flex flex-wrap items-center gap-2 sm:col-span-4">
            <Button type="submit" size="sm" disabled={save.isPending}>
              <Trans>Save counts</Trans>
            </Button>
            <Button
              type="button"
              size="sm"
              variant="secondary"
              disabled={!saved?.recorded_at}
              onClick={() => onRecordDecision(item, hint)}
            >
              <Trans>Record decision</Trans>
            </Button>
          </div>
        </form>
        <div className="flex flex-col gap-1 text-sm" data-testid="threshold-evaluation">
          {evaln.neededYes !== null ? (
            <span>
              <Trans>
                Needed: at least {evaln.neededYes} yes votes
                {evaln.neededSharePct !== null ? ` and more than ${evaln.neededSharePct}% of the land share` : ''}
              </Trans>
              {' · '}
              {evaln.met === null ? (
                <Trans>enter the land share to finish the check</Trans>
              ) : evaln.met ? (
                <span className="font-medium text-success">
                  <Trans>met</Trans>
                </span>
              ) : (
                <span className="font-medium text-destructive">
                  <Trans>not met</Trans>
                </span>
              )}
            </span>
          ) : rule === 'simple_majority_present' ? (
            <span>
              <Trans>More yes than no among those present</Trans>
              {' · '}
              {evaln.met ? <Trans>met</Trans> : <Trans>not met</Trans>}
            </span>
          ) : (
            <span className="text-muted-foreground">
              <Trans>No threshold is set for this topic.</Trans>
            </span>
          )}
          {saved?.recorded_at ? (
            <span className="flex items-center gap-2">
              <Trans>Server evaluation:</Trans>
              <Badge variant={OUTCOME_VARIANT[hint] ?? 'secondary'}>{labels.outcome(hint)}</Badge>
            </span>
          ) : null}
        </div>
        <FormRootError message={form.formState.errors.root?.message} />
      </CardContent>
    </Card>
  )
}

// ------------------------------------------------------------------ record a decision

export function DecisionDialog({
  ctx,
  meetingId,
  item,
  hint,
  onClose,
}: {
  ctx: SiteCtx
  meetingId: string
  item: S['AgendaItem'] | null
  hint: S['VoteCount']['outcome_hint']
  onClose: () => void
}) {
  const { t } = useLingui()
  const labels = useGovLabels()
  const idem = useIdempotencyKey()
  const persons = usePersons(ctx.orgId)
  const schema = useMemo(
    () =>
      z.object({
        text: z.string().trim().min(1, t`Write the decision.`),
        outcome: z.enum(['accepted', 'rejected', 'no_vote', 'noted']),
        concerns_residents: z.boolean(),
        dissents: z.array(
          z.object({
            person_id: z.string().min(1, t`Choose a person.`),
            text: z.string().trim().min(1, t`Write the dissent.`),
          }),
        ),
      }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      text: item?.title ?? '',
      outcome: hint === 'accepted' ? 'accepted' : hint === 'rejected' ? 'rejected' : item?.will_be_voted ? 'no_vote' : 'noted',
      concerns_residents: false,
      dissents: [],
    },
  })
  const dissents = useFieldArray({ control: form.control, name: 'dissents' })
  const create = useOrgMutation(async (v: Values) =>
    unwrap(
      gApi.POST('/v1/orgs/{org_id}/decisions', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: {
          site_id: ctx.siteId,
          meeting_id: meetingId,
          agenda_item_id: item?.id ?? null,
          text: v.text.trim(),
          outcome: v.outcome,
          concerns_residents: v.concerns_residents,
          dissents: v.dissents.map((d) => ({ person_id: d.person_id, text: d.text.trim() })),
        },
      }),
    ),
  )
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent className="sm:max-w-xl">
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={form.handleSubmit(async (v) => {
            try {
              const d = await create.mutateAsync(v)
              toast.info(t`Decision ${decisionNo(d)} recorded.`)
              onClose()
            } catch (e) {
              applyServerErrors(form, e, t`Could not record the decision.`)
            }
          })}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>Record decision</Trans>
            </DialogTitle>
            <DialogDescription>
              {item ? item.title : <Trans>A decision without an agenda item.</Trans>}
            </DialogDescription>
          </DialogHeader>
          <Field label={t`Decision text`} htmlFor="dec-text" error={form.formState.errors.text?.message}>
            <Textarea id="dec-text" {...form.register('text')} />
          </Field>
          <Field label={t`Outcome`} htmlFor="dec-outcome">
            <Select id="dec-outcome" {...form.register('outcome')}>
              {(['accepted', 'rejected', 'no_vote', 'noted'] as const).map((o) => (
                <option key={o} value={o}>
                  {labels.outcome(o)}
                </option>
              ))}
            </Select>
          </Field>
          <fieldset className="flex flex-col gap-2">
            <legend className="text-sm font-medium">
              <Trans>Dissenting opinions (muhalefet şerhi)</Trans>
            </legend>
            {dissents.fields.map((f, i) => (
              <div key={f.id} className="grid gap-2 sm:grid-cols-[12rem_1fr_auto]">
                <Select aria-label={t`Dissenting owner`} {...form.register(`dissents.${i}.person_id`)}>
                  <option value="">{t`Choose a person`}</option>
                  {(persons.data ?? []).map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.display_name}
                    </option>
                  ))}
                </Select>
                <Input aria-label={t`Dissent text`} {...form.register(`dissents.${i}.text`)} />
                <Button type="button" variant="ghost" size="icon" aria-label={t`Remove dissent`} onClick={() => dissents.remove(i)}>
                  <Trash2Icon />
                </Button>
              </div>
            ))}
            <div>
              <Button type="button" variant="outline" size="sm" onClick={() => dissents.append({ person_id: '', text: '' })}>
                <PlusIcon />
                <Trans>Add dissent</Trans>
              </Button>
            </div>
          </fieldset>
          <Checkbox
            label={t`Concerns residents (tenants can see it)`}
            {...form.register('concerns_residents')}
          />
          <FormRootError message={form.formState.errors.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={create.isPending}>
              <Trans>Record</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ------------------------------------------------------------------ rows and the panel

export function DecisionRow({
  ctx,
  decision,
  agendaTopic,
  canApply,
  footer,
}: {
  ctx: SiteCtx
  decision: S['Decision']
  agendaTopic?: string | null
  canApply: boolean
  footer?: React.ReactNode
}) {
  const labels = useGovLabels()
  const [applying, setApplying] = useState(false)
  const suggested = effectKindForTopic(agendaTopic)
  return (
    <li className="flex flex-col gap-2 rounded-md border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-sm font-semibold" dir="ltr">
          {decisionNo(decision)}
        </span>
        <Badge variant={OUTCOME_VARIANT[decision.outcome] ?? 'secondary'}>{labels.outcome(decision.outcome)}</Badge>
        {decision.concerns_residents ? (
          <Badge variant="outline">
            <Trans>Concerns residents</Trans>
          </Badge>
        ) : null}
        {decision.effect ? (
          <Badge variant="success">
            <Trans>Applied: {labels.effect(decision.effect.kind)}</Trans>
          </Badge>
        ) : null}
        {canApply && decision.outcome === 'accepted' && !decision.effect ? (
          <Button size="sm" variant={suggested ? 'default' : 'outline'} className="ms-auto" onClick={() => setApplying(true)}>
            <Trans>Apply effect</Trans>
          </Button>
        ) : null}
      </div>
      <p className="text-sm">{decision.text}</p>
      {decision.dissents.length > 0 ? (
        <ul className="list-disc ps-5 text-sm text-muted-foreground">
          {decision.dissents.map((d) => (
            <li key={d.person_id}>
              {d.display_name ?? d.person_id}: {d.text}
            </li>
          ))}
        </ul>
      ) : null}
      {footer}
      {applying ? (
        <ApplyDecisionDialog ctx={ctx} decision={decision} suggestedKind={suggested} onClose={() => setApplying(false)} />
      ) : null}
    </li>
  )
}

/** Counts and decisions of an assembly in session or closed (G1, G5). */
export function DecisionsPanel({ ctx, meeting }: { ctx: SiteCtx; meeting: S['Meeting'] }) {
  const { t } = useLingui()
  const { can } = useSession()
  const agenda = useAgenda(ctx.orgId, meeting.id)
  const owners = useOwnerRows(ctx.orgId, ctx.siteId)
  const decisions = useDecisions(ctx.orgId, ctx.siteId, { meeting_id: meeting.id })
  const [recording, setRecording] = useState<{ item: S['AgendaItem'] | null; hint: S['VoteCount']['outcome_hint'] } | null>(null)
  const canRecord = can('decisions.manage')
  const eligibleUnits = owners.rows.reduce((n, o) => n + o.units.length, 0)
  const voted = (agenda.data ?? []).filter((a) => a.will_be_voted)
  const active = meeting.status === 'in_session' || meeting.status === 'closed'
  const topics = new Map((agenda.data ?? []).map((a) => [a.id, a.topic]))
  return (
    <div className="flex flex-col gap-4">
      {!active ? (
        <Callout tone="info" role="status">
          <Trans>Counts and decisions are recorded once the assembly is in session.</Trans>
        </Callout>
      ) : null}
      {active && canRecord
        ? voted.map((a) => (
            <VoteCountsCard
              key={a.id}
              ctx={ctx}
              item={a}
              eligibleUnits={eligibleUnits}
              onRecordDecision={(item, hint) => setRecording({ item, hint })}
            />
          ))
        : null}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-2">
          <CardTitle>
            <Trans>Decisions</Trans>
          </CardTitle>
          {active && canRecord ? (
            <Button size="sm" variant="outline" onClick={() => setRecording({ item: null, hint: 'undetermined' })}>
              <Trans>Record a note</Trans>
            </Button>
          ) : null}
        </CardHeader>
        <CardContent>
          {decisions.isError ? (
            <p role="alert" className="text-sm text-destructive">
              {t`Could not load the decisions.`}
            </p>
          ) : (decisions.data ?? []).length === 0 ? (
            <p className="text-sm text-muted-foreground">
              <Trans>No decisions recorded yet.</Trans>
            </p>
          ) : (
            <ul className="flex flex-col gap-2">
              {(decisions.data ?? []).map((d) => (
                <DecisionRow
                  key={d.id}
                  ctx={ctx}
                  decision={d}
                  agendaTopic={d.agenda_item_id ? topics.get(d.agenda_item_id) : null}
                  canApply={canRecord}
                />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
      {recording ? (
        <DecisionDialog
          ctx={ctx}
          meetingId={meeting.id}
          item={recording.item}
          hint={recording.hint}
          onClose={() => setRecording(null)}
        />
      ) : null}
    </div>
  )
}

