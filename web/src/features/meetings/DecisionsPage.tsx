import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { Input } from '@/components/ui/input'
import { useDecisions } from './hooks'
import { useSiteCtx } from './kit/api'
import { PageHeader, QueryBoundary, Select } from './kit/ui'
import { DecisionRow } from './DecisionsPanel'
import { useGovLabels } from './labels'

/** Searchable decision history with the per-site, per-year sequence numbers (G1). */
export function DecisionsPage() {
  const { t } = useLingui()
  const labels = useGovLabels()
  const { can } = useSession()
  const ctx = useSiteCtx()!
  const [q, setQ] = useState('')
  const [outcome, setOutcome] = useState('')
  const [year, setYear] = useState('')
  const decisions = useDecisions(ctx.orgId, ctx.siteId, {
    ...(q.trim() ? { q: q.trim() } : {}),
    ...(outcome ? { outcome } : {}),
    ...(year ? { year: Number(year) } : {}),
  })
  const thisYear = new Date().getFullYear()
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Decision history`}
        description={t`Numbered drafts to copy into the notarized decision book (karar defteri); this list is not the book itself.`}
      />
      <div className="flex flex-wrap gap-2">
        <Input
          aria-label={t`Search decisions`}
          placeholder={t`Search the text`}
          className="max-w-xs"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <Select
          aria-label={t`Outcome`}
          className="w-auto"
          value={outcome}
          onChange={(e) => setOutcome(e.target.value)}
        >
          <option value="">{t`Any outcome`}</option>
          {['accepted', 'rejected', 'no_vote', 'noted'].map((o) => (
            <option key={o} value={o}>
              {labels.outcome(o)}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t`Year`}
          className="w-auto"
          value={year}
          onChange={(e) => setYear(e.target.value)}
        >
          <option value="">{t`Any year`}</option>
          {[thisYear, thisYear - 1, thisYear - 2].map((y) => (
            <option key={y} value={y}>
              {y}
            </option>
          ))}
        </Select>
      </div>
      <QueryBoundary
        query={decisions}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No decisions found`}
        emptyDescription={t`Decisions appear here when they are recorded at an assembly.`}
      >
        {(items) => (
          <ul className="flex flex-col gap-2">
            {items.map((d) => (
              <DecisionRow
                key={d.id}
                ctx={ctx}
                decision={d}
                canApply={can('decisions.manage')}
                footer={
                  <Link
                    to="/orgs/$orgId/meetings/$meetingId"
                    params={{ orgId: ctx.orgId, meetingId: d.meeting_id }}
                    className="text-xs text-muted-foreground underline-offset-4 hover:underline"
                  >
                    <Trans>Open the assembly</Trans>
                  </Link>
                }
              />
            ))}
          </ul>
        )}
      </QueryBoundary>
    </div>
  )
}
