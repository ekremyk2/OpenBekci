import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { ArrowDownIcon, ArrowUpIcon, PlusIcon, RotateCcwIcon, Trash2Icon } from 'lucide-react'
import { apiDraft, ifMatch, type TicketWorkflow } from '@/api/draft'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { ConfirmDialog } from '@/components/data/ConfirmDialog'
import { FormError } from '@/components/forms/FormField'
import { must } from '@/lib/pagination'
import { toast } from '@/lib/toast'
import { useApplyErrorMessage } from './errors'

type Semantic = 'open_like' | 'closed_like'

export interface Row {
  uid: string
  id?: string
  key: string | null
  name: string
  semantic: Semantic
}

export function rowsFromWorkflow(w: TicketWorkflow): Row[] {
  return [...w.statuses]
    .sort((a, b) => a.position - b.position)
    .map((s) => ({ uid: s.id, id: s.id, key: s.key, name: s.name, semantic: s.semantic }))
}

export function move(rows: Row[], from: number, to: number): Row[] {
  if (to < 0 || to >= rows.length) return rows
  const next = [...rows]
  const [item] = next.splice(from, 1)
  next.splice(to, 0, item!)
  return next
}

/** The documented default flow (O1): open → in progress → done. */
const DEFAULT_KEYS = ['open', 'in_progress', 'done'] as const

/** Validation messages are codes so the component can translate them. */
export function validateRows(
  rows: Row[],
): 'empty_name' | 'duplicate' | 'no_open' | 'no_closed' | null {
  if (rows.some((r) => !r.name.trim())) return 'empty_name'
  const names = rows.map((r) => r.name.trim().toLowerCase())
  if (new Set(names).size !== names.length) return 'duplicate'
  if (!rows.some((r) => r.semantic === 'open_like')) return 'no_open'
  if (!rows.some((r) => r.semantic === 'closed_like')) return 'no_closed'
  return null
}

export function WorkflowEditor({
  orgId,
  siteId,
  workflow,
}: {
  orgId: string
  siteId: string
  workflow: TicketWorkflow
}) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const describe = useApplyErrorMessage()
  const [rows, setRows] = useState<Row[]>(() => rowsFromWorkflow(workflow))
  const [replacements, setReplacements] = useState<Record<string, string>>({})
  const [error, setError] = useState<string>()
  // "Restore default" only proposes; nothing changes or is saved until the person confirms
  const [proposal, setProposal] = useState<{
    rows: Row[]
    replacements: Record<string, string>
    moves: { from: string; to: string }[]
  } | null>(null)

  const removed = workflow.statuses.filter((s) => !rows.some((r) => r.id === s.id))
  const kept = rows.filter((r) => r.id)
  const problem = validateRows(rows)
  const dirty =
    JSON.stringify(rows.map((r) => [r.id, r.name, r.semantic])) !==
    JSON.stringify(rowsFromWorkflow(workflow).map((r) => [r.id, r.name, r.semantic]))

  const save = useMutation({
    mutationFn: async (input: { rows: Row[]; replacements: Record<string, string> }) =>
      must(
        await apiDraft.PUT('/v1/orgs/{org_id}/ticket-workflow', {
          params: {
            path: { org_id: orgId },
            query: { site_id: siteId },
            header: ifMatch(workflow.version),
          },
          body: {
            statuses: input.rows.map((r) => ({
              id: r.id,
              name: r.name.trim(),
              semantic: r.semantic,
            })),
            replacements: input.replacements,
          },
        }),
      ),
    onSuccess: async () => {
      setError(undefined)
      await qc.invalidateQueries({ queryKey: ['operations.getTicketWorkflow'] })
      toast.info(t`Workflow saved.`)
    },
    onError: (e) => setError(describe(e)),
  })

  function submit(next: Row[], nextReplacements: Record<string, string>) {
    const gone = workflow.statuses.filter((s) => !next.some((r) => r.id === s.id))
    const missing = gone.find((s) => !nextReplacements[s.id])
    if (missing) {
      setError(t`Choose where the tickets of "${missing.name}" move to.`)
      return
    }
    save.mutate({ rows: next, replacements: nextReplacements })
  }

  /** The default flow with the default names (a renamed default status gets its name back). */
  function proposeDefault() {
    const names = { open: t`Open`, in_progress: t`In progress`, done: t`Done` }
    const defaults: Row[] = DEFAULT_KEYS.map((key) => {
      const existing = workflow.statuses.find((s) => s.key === key)
      return {
        uid: existing?.id ?? `new-${key}`,
        id: existing?.id,
        key,
        name: names[key],
        semantic: key === 'done' ? 'closed_like' : 'open_like',
      }
    })
    const auto: Record<string, string> = {}
    const moves: { from: string; to: string }[] = []
    const open = defaults[0]
    const done = defaults[2]
    for (const s of workflow.statuses) {
      if (defaults.some((d) => d.id === s.id)) continue
      const target = s.semantic === 'closed_like' ? done : open
      if (target?.id) {
        auto[s.id] = target.id
        moves.push({ from: s.name, to: target.name })
      }
    }
    setProposal({ rows: defaults, replacements: auto, moves })
  }

  function confirmDefault() {
    if (!proposal) return
    setRows(proposal.rows)
    setReplacements(proposal.replacements)
    submit(proposal.rows, proposal.replacements)
    setProposal(null)
  }

  return (
    <form
      className="flex flex-col gap-4"
      aria-label={t`Ticket workflow`}
      onSubmit={(e) => {
        e.preventDefault()
        if (!problem) submit(rows, replacements)
      }}
    >
      <p className="text-sm text-muted-foreground">
        {workflow.is_default ? (
          <Trans>This site uses the default workflow. Edit it to add your own statuses.</Trans>
        ) : (
          <Trans>This site has its own workflow.</Trans>
        )}{' '}
        <Trans>
          Open-like statuses count as open tickets in reports; closed-like statuses count as closed.
        </Trans>
      </p>
      <ol className="flex flex-col gap-2">
        {rows.map((row, i) => (
          <li key={row.uid} className="flex flex-wrap items-center gap-2 rounded-md border p-2">
            <span className="w-6 text-center text-sm text-muted-foreground">{i + 1}</span>
            <Input
              className="min-w-40 flex-1"
              aria-label={t`Status name ${i + 1}`}
              value={row.name}
              onChange={(e) =>
                setRows((rs) =>
                  rs.map((r) => (r.uid === row.uid ? { ...r, name: e.target.value } : r)),
                )
              }
            />
            <Select
              className="w-44"
              aria-label={t`Meaning of status ${i + 1}`}
              value={row.semantic}
              onChange={(e) =>
                setRows((rs) =>
                  rs.map((r) =>
                    r.uid === row.uid ? { ...r, semantic: e.target.value as Semantic } : r,
                  ),
                )
              }
            >
              <option value="open_like">{t`Open-like`}</option>
              <option value="closed_like">{t`Closed-like`}</option>
            </Select>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              disabled={i === 0}
              onClick={() => setRows((rs) => move(rs, i, i - 1))}
              aria-label={t`Move ${row.name} up`}
            >
              <ArrowUpIcon />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              disabled={i === rows.length - 1}
              onClick={() => setRows((rs) => move(rs, i, i + 1))}
              aria-label={t`Move ${row.name} down`}
            >
              <ArrowDownIcon />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              disabled={rows.length <= 2}
              onClick={() => setRows((rs) => rs.filter((r) => r.uid !== row.uid))}
              aria-label={t`Remove ${row.name}`}
            >
              <Trash2Icon />
            </Button>
          </li>
        ))}
      </ol>

      {removed.length > 0 ? (
        <fieldset className="flex flex-col gap-2 rounded-md border border-dashed p-3">
          <legend className="px-1 text-sm font-medium">
            <Trans>Where do tickets of removed statuses go?</Trans>
          </legend>
          {removed.map((s) => (
            <div key={s.id} className="flex flex-wrap items-center gap-2 text-sm">
              <span className="min-w-32">{s.name}</span>
              <Select
                className="w-56"
                aria-label={t`Replacement for ${s.name}`}
                value={replacements[s.id] ?? ''}
                onChange={(e) => setReplacements((r) => ({ ...r, [s.id]: e.target.value }))}
              >
                <option value="">{t`Choose a status`}</option>
                {kept.map((k) => (
                  <option key={k.id} value={k.id}>
                    {k.name}
                  </option>
                ))}
              </Select>
            </div>
          ))}
        </fieldset>
      ) : null}

      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          variant="outline"
          onClick={() =>
            setRows((rs) => [
              ...rs,
              { uid: crypto.randomUUID(), key: null, name: '', semantic: 'open_like' },
            ])
          }
        >
          <PlusIcon aria-hidden="true" />
          <Trans>Add status</Trans>
        </Button>
        <Button type="button" variant="ghost" onClick={proposeDefault}>
          <RotateCcwIcon aria-hidden="true" />
          <Trans>Restore default workflow</Trans>
        </Button>
        <Button type="submit" className="ms-auto" disabled={!dirty || !!problem || save.isPending}>
          <Trans>Save workflow</Trans>
        </Button>
      </div>
      {problem && dirty ? (
        <p role="alert" className="text-sm text-destructive">
          {problem === 'empty_name' ? (
            <Trans>Every status needs a name.</Trans>
          ) : problem === 'duplicate' ? (
            <Trans>Status names must be different.</Trans>
          ) : problem === 'no_open' ? (
            <Trans>Keep at least one open-like status.</Trans>
          ) : (
            <Trans>Keep at least one closed-like status.</Trans>
          )}
        </p>
      ) : null}
      <FormError message={error} />
      <ConfirmDialog
        open={proposal !== null}
        onOpenChange={(open) => !open && setProposal(null)}
        title={<Trans>Restore the default workflow?</Trans>}
        description={
          <Trans>
            The statuses below replace the current ones and are saved right away. Nothing changes if
            you cancel.
          </Trans>
        }
        confirmLabel={<Trans>Restore and save</Trans>}
        busy={save.isPending}
        onConfirm={confirmDefault}
      >
        {proposal ? (
          <div className="flex flex-col gap-2 text-sm">
            <ol className="list-decimal ps-5">
              {proposal.rows.map((r) => (
                <li key={r.uid}>{r.name}</li>
              ))}
            </ol>
            {proposal.moves.length > 0 ? (
              <ul className="list-disc ps-5">
                {proposal.moves.map((m) => (
                  <li key={m.from}>
                    <Trans>
                      Tickets of "{m.from}" move to "{m.to}".
                    </Trans>
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}
      </ConfirmDialog>
    </form>
  )
}
