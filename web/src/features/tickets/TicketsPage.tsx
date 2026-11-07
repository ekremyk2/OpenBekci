import { useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { LifeBuoyIcon, MessageSquareIcon, PlusIcon, ThumbsUpIcon } from 'lucide-react'
import { apiDraft } from '@/api/draft'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { SiteDate } from '@/components/data/SiteDate'
import { TableSkeleton } from '@/components/data/Skeletons'
import { buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { LoadMore, must, useCursorQuery } from '@/lib/pagination'
import { cn } from '@/lib/utils'
import { SitePicker, useSite } from '@/features/dashboard/site'
import { useStaff, useUnits, useVendors } from '@/features/dashboard/reference'
import { StatusBadge, VisibilityBadge } from './badges'
import { useCategories, useWorkflow } from './queries'

export type TicketView = 'all' | 'mine' | 'common' | 'queue'

export interface TicketSearch {
  view?: TicketView
  status?: string
  category?: string
  /** `p:<person id>` or `v:<vendor id>` */
  assignee?: string
  unit?: string
  visibility?: 'private' | 'common'
  q?: string
}

export function parseTicketSearch(raw: Record<string, unknown>): TicketSearch {
  const str = (v: unknown) => (typeof v === 'string' && v ? v : undefined)
  const view = str(raw.view)
  const visibility = str(raw.visibility)
  return {
    view:
      view === 'all' || view === 'mine' || view === 'common' || view === 'queue' ? view : undefined,
    status: str(raw.status),
    category: str(raw.category),
    assignee: str(raw.assignee),
    unit: str(raw.unit),
    visibility: visibility === 'private' || visibility === 'common' ? visibility : undefined,
    q: str(raw.q),
  }
}

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return v
}

export function TicketsPage({
  view: forcedView,
  search,
  onSearch,
}: {
  /** The staff queue route pins the view. */
  view?: TicketView
  search: TicketSearch
  onSearch: (next: TicketSearch) => void
}) {
  const { t } = useLingui()
  const { org, can } = useSession()
  const { site } = useSite()
  const orgId = org?.id ?? ''
  const canReadAll = can('tickets.read')
  const canManage = can('tickets.manage')
  const canWork = can('tickets.work')
  const canOpen = can('tickets.open')
  const view: TicketView = forcedView ?? search.view ?? (canReadAll ? 'all' : 'mine')

  const [text, setText] = useState(search.q ?? '')
  const q = useDebounced(text, 300)
  useEffect(() => {
    if ((search.q ?? '') !== q) onSearch({ ...search, q: q || undefined })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q])

  const workflow = useWorkflow(orgId, site?.id)
  const categories = useCategories(orgId, site?.id)
  const units = useUnits(orgId, site?.id, canManage)
  const staff = useStaff(orgId, canManage)
  const vendors = useVendors(orgId, canManage)

  const filters = {
    status_id: search.status,
    category_id: search.category,
    unit_id: search.unit,
    visibility: view === 'common' ? ('common' as const) : search.visibility,
    assignee_person_id: search.assignee?.startsWith('p:') ? search.assignee.slice(2) : undefined,
    assignee_vendor_id: search.assignee?.startsWith('v:') ? search.assignee.slice(2) : undefined,
    mine: view === 'mine' ? true : undefined,
    queue: view === 'queue' ? true : undefined,
    q: search.q,
  }

  const list = useCursorQuery(
    ['operations.listTickets', orgId, site?.id, view, filters],
    async (cursor) =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/tickets', {
          params: {
            path: { org_id: orgId },
            query: { site_id: site!.id, ...filters, limit: 25, cursor },
          },
        }),
      ),
    { enabled: !!site },
  )

  if (!canReadAll && !canOpen && !canWork && !can('tickets.read_assigned')) {
    return (
      <EmptyState
        icon={LifeBuoyIcon}
        title={<Trans>You do not have access to tickets</Trans>}
        description={<Trans>Ask your manager if you think you should.</Trans>}
      />
    )
  }

  const tabs: { id: TicketView; label: string; show: boolean }[] = [
    { id: 'all', label: t`All tickets`, show: canReadAll },
    { id: 'mine', label: t`My tickets`, show: canOpen },
    { id: 'common', label: t`Common areas`, show: canOpen || canReadAll },
    { id: 'queue', label: t`Staff queue`, show: canWork },
  ]
  const tz = site?.timezone ?? 'UTC'

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="me-auto text-2xl font-semibold">
          <Trans>Tickets</Trans>
        </h1>
        <SitePicker />
        {canManage ? (
          <Link
            to="/orgs/$orgId/settings/tickets"
            params={{ orgId }}
            className={buttonVariants({ variant: 'outline' })}
          >
            <Trans>Workflow and categories</Trans>
          </Link>
        ) : null}
        {canOpen ? (
          <Link to="/orgs/$orgId/tickets/new" params={{ orgId }} className={buttonVariants()}>
            <PlusIcon aria-hidden="true" />
            <Trans>New ticket</Trans>
          </Link>
        ) : null}
      </div>

      <div role="tablist" aria-label={t`Ticket views`} className="flex flex-wrap gap-1 border-b">
        {tabs
          .filter((x) => x.show)
          .map((tab) => (
            <button
              key={tab.id}
              role="tab"
              type="button"
              aria-selected={view === tab.id}
              onClick={() => onSearch({ ...search, view: tab.id })}
              className={cn(
                '-mb-px border-b-2 px-3 py-2 text-sm font-medium outline-none focus-visible:ring-[3px] focus-visible:ring-ring',
                view === tab.id
                  ? 'border-primary text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground',
              )}
            >
              {tab.label}
            </button>
          ))}
      </div>

      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-6">
        <Input
          type="search"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={t`Search tickets`}
          aria-label={t`Search tickets`}
          className="lg:col-span-2"
        />
        <Select
          aria-label={t`Status`}
          value={search.status ?? ''}
          onChange={(e) => onSearch({ ...search, status: e.target.value || undefined })}
        >
          <option value="">{t`Any status`}</option>
          {workflow.data?.statuses.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t`Category`}
          value={search.category ?? ''}
          onChange={(e) => onSearch({ ...search, category: e.target.value || undefined })}
        >
          <option value="">{t`Any category`}</option>
          {categories.data?.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </Select>
        {view !== 'common' ? (
          <Select
            aria-label={t`Visibility`}
            value={search.visibility ?? ''}
            onChange={(e) =>
              onSearch({
                ...search,
                visibility: (e.target.value as 'private' | 'common') || undefined,
              })
            }
          >
            <option value="">{t`Private and common`}</option>
            <option value="private">{t`Private`}</option>
            <option value="common">{t`Common`}</option>
          </Select>
        ) : null}
        {canManage ? (
          <>
            <Select
              aria-label={t`Assignee`}
              value={search.assignee ?? ''}
              onChange={(e) => onSearch({ ...search, assignee: e.target.value || undefined })}
            >
              <option value="">{t`Anyone`}</option>
              {staff.data?.map((m) => (
                <option key={m.person_id} value={`p:${m.person_id}`}>
                  {m.person_display_name}
                </option>
              ))}
              {vendors.data?.map((v) => (
                <option key={v.id} value={`v:${v.id}`}>
                  {v.name}
                </option>
              ))}
            </Select>
            <Select
              aria-label={t`Unit`}
              value={search.unit ?? ''}
              onChange={(e) => onSearch({ ...search, unit: e.target.value || undefined })}
            >
              <option value="">{t`Any unit`}</option>
              {units.data?.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.door_number}
                </option>
              ))}
            </Select>
          </>
        ) : null}
      </div>

      {list.isPending ? (
        <TableSkeleton rows={5} columns={3} />
      ) : list.isError ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : list.items.length === 0 ? (
        <EmptyState
          icon={LifeBuoyIcon}
          title={<Trans>No tickets here</Trans>}
          description={
            view === 'queue' ? (
              <Trans>Nothing is waiting for you.</Trans>
            ) : (
              <Trans>Tickets matching these filters will appear here.</Trans>
            )
          }
          action={
            canOpen ? (
              <Link to="/orgs/$orgId/tickets/new" params={{ orgId }} className={buttonVariants()}>
                <Trans>New ticket</Trans>
              </Link>
            ) : undefined
          }
        />
      ) : (
        <>
          <ul className="flex flex-col divide-y rounded-lg border" aria-label={t`Tickets`}>
            {list.items.map((ticket) => (
              <li key={ticket.id}>
                <Link
                  to="/orgs/$orgId/tickets/$ticketId"
                  params={{ orgId, ticketId: ticket.id }}
                  className="flex flex-col gap-1 p-3 outline-none hover:bg-muted/50 focus-visible:ring-[3px] focus-visible:ring-ring"
                >
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{ticket.title}</span>
                    <StatusBadge name={ticket.status_name} semantic={ticket.status_semantic} />
                    <VisibilityBadge visibility={ticket.visibility} />
                    {ticket.merged_into_id ? (
                      <span className="text-xs text-muted-foreground">
                        <Trans>Merged</Trans>
                      </span>
                    ) : null}
                  </div>
                  <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
                    {ticket.unit_door_number ? <span>{ticket.unit_door_number}</span> : null}
                    <span>{ticket.reporter_display_name}</span>
                    <SiteDate value={ticket.created_at} timeZone={tz} kind="datetime" />
                    {ticket.assignee ? (
                      <span>
                        <Trans>Assigned to {ticket.assignee.display_name}</Trans>
                      </span>
                    ) : null}
                    {ticket.visibility === 'common' || ticket.vote_count > 0 ? (
                      <span className="inline-flex items-center gap-1">
                        <ThumbsUpIcon className="size-3" aria-hidden="true" />
                        <span aria-label={t`${ticket.vote_count} people confirmed this`}>
                          +{ticket.vote_count}
                        </span>
                      </span>
                    ) : null}
                    {ticket.comment_count > 0 ? (
                      <span className="inline-flex items-center gap-1">
                        <MessageSquareIcon className="size-3" aria-hidden="true" />
                        {ticket.comment_count}
                      </span>
                    ) : null}
                  </div>
                </Link>
              </li>
            ))}
          </ul>
          <LoadMore query={list} />
        </>
      )}
    </div>
  )
}
