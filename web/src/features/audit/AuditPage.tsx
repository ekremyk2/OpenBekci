import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { ChevronDownIcon, ChevronRightIcon, LockIcon, ScrollTextIcon } from 'lucide-react'
import { draftApi, type AuditEvent } from '@/api/draft'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { SiteDate } from '@/components/data/SiteDate'
import { TableSkeleton } from '@/components/data/Skeletons'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useSite } from '@/features/dashboard/site'
import { LoadMore, must, useCursorQuery } from '@/lib/pagination'
import { zonedLocalToIso } from '@/lib/zoned'
import { formatValue, parseDiff } from './diff'
import type { AuditSearch } from './search'

/** Values show as text; hashed PII fields are marked and never rendered as values. */
export function DiffView({ diff }: { diff: unknown }) {
  const { t } = useLingui()
  const entries = parseDiff(diff)
  if (entries.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        <Trans>No details recorded.</Trans>
      </p>
    )
  }
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm" aria-label={t`Changes`}>
      {entries.map((e) => (
        <div key={e.field} className="contents">
          <dt className="font-mono text-xs text-muted-foreground">{e.field}</dt>
          <dd className="flex flex-wrap items-center gap-2">
            {e.kind === 'hashed' ? (
              <>
                <Badge variant="warning">
                  <LockIcon aria-hidden="true" />
                  <Trans>Changed (private data, hashed)</Trans>
                </Badge>
                <code className="text-xs text-muted-foreground" title={t`Hash before and after`}>
                  {e.before ?? '—'} → {e.after ?? '—'}
                </code>
              </>
            ) : e.kind === 'changed' ? (
              <>
                <del className="text-destructive">{formatValue(e.before)}</del>
                <span aria-hidden="true">→</span>
                <ins className="text-success no-underline">{formatValue(e.after)}</ins>
              </>
            ) : (
              <span>{formatValue(e.value)}</span>
            )}
          </dd>
        </div>
      ))}
    </dl>
  )
}

function EventRow({ event, timeZone }: { event: AuditEvent; timeZone: string }) {
  const [open, setOpen] = useState(false)
  const { t } = useLingui()
  return (
    <li className="p-3">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex w-full flex-wrap items-center gap-2 text-start outline-none focus-visible:ring-[3px] focus-visible:ring-ring"
      >
        {open ? (
          <ChevronDownIcon className="size-4" aria-hidden="true" />
        ) : (
          <ChevronRightIcon className="size-4 rtl-flip" aria-hidden="true" />
        )}
        <span className="font-mono text-sm font-medium">{event.action}</span>
        <Badge variant="outline">{event.target_type}</Badge>
        <span className="text-sm text-muted-foreground">
          {event.actor_display_name ?? t`System`}
        </span>
        <SiteDate
          value={event.at}
          timeZone={timeZone}
          kind="datetime"
          className="ms-auto text-xs text-muted-foreground"
        />
      </button>
      {open ? (
        <div className="mt-3 ms-6 flex flex-col gap-3">
          <DiffView diff={event.diff} />
          <p className="text-xs text-muted-foreground">
            <Trans>
              Request {event.request_id}
              {event.ip ? ` from ${event.ip}` : ''}
            </Trans>
          </p>
        </div>
      ) : null}
    </li>
  )
}

export function AuditPage({
  search,
  onSearch,
}: {
  search: AuditSearch
  onSearch: (s: AuditSearch) => void
}) {
  const { t } = useLingui()
  const { org, can } = useSession()
  const { site } = useSite()
  const orgId = org?.id ?? ''
  const tz = site?.timezone ?? 'UTC'
  const [draft, setDraft] = useState(search)
  const allowed = can('audit.read')

  const list = useCursorQuery<AuditEvent>(
    ['audit.listEvents', orgId, search],
    async (cursor) =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/audit-events', {
          params: {
            path: { org_id: orgId },
            query: {
              action: search.action,
              target_type: search.target,
              actor_user_id: search.actor,
              from: search.from ? zonedLocalToIso(`${search.from}T00:00`, tz) : undefined,
              to: search.to ? zonedLocalToIso(`${search.to}T23:59`, tz) : undefined,
              limit: 25,
              cursor,
            },
          },
        }),
      ),
    { enabled: allowed },
  )

  if (!allowed) {
    return (
      <EmptyState
        icon={LockIcon}
        title={<Trans>You do not have access to the audit log</Trans>}
        description={<Trans>Ask your manager if you think you should.</Trans>}
      />
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <h2 className="text-lg font-semibold">
        <Trans>Audit log</Trans>
      </h2>
      <form
        className="grid gap-2 sm:grid-cols-2 lg:grid-cols-5"
        onSubmit={(e) => {
          e.preventDefault()
          onSearch(draft)
        }}
      >
        {(
          [
            ['action', t`Action`, 'text'],
            ['target', t`Target type`, 'text'],
            ['actor', t`Actor (user id)`, 'text'],
            ['from', t`From`, 'date'],
            ['to', t`To`, 'date'],
          ] as const
        ).map(([key, label, type]) => (
          <div key={key} className="flex flex-col gap-1">
            <Label htmlFor={`audit-${key}`}>{label}</Label>
            <Input
              id={`audit-${key}`}
              type={type}
              value={draft[key] ?? ''}
              onChange={(e) => setDraft({ ...draft, [key]: e.target.value || undefined })}
            />
          </div>
        ))}
        <div className="flex gap-2 sm:col-span-2 lg:col-span-5">
          <Button type="submit">
            <Trans>Apply filters</Trans>
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setDraft({})
              onSearch({})
            }}
          >
            <Trans>Clear</Trans>
          </Button>
        </div>
      </form>

      {list.isPending ? (
        <TableSkeleton rows={6} columns={3} />
      ) : list.isError ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : list.items.length === 0 ? (
        <EmptyState
          icon={ScrollTextIcon}
          title={<Trans>No events match</Trans>}
          description={<Trans>Try widening the filters.</Trans>}
        />
      ) : (
        <>
          <ul className="flex flex-col divide-y rounded-lg border" aria-label={t`Audit events`}>
            {list.items.map((e) => (
              <EventRow key={e.id} event={e} timeZone={tz} />
            ))}
          </ul>
          <LoadMore query={list} />
        </>
      )}
    </div>
  )
}
