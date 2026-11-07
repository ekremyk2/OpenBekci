import { Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { PlusIcon, UsersIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { Schema } from '@/api/draft'
import { useSession } from '@/app/session'
import { DataTable, ListEmpty, useCursorPages } from '@/components/data/DataTable'
import { PageHeader } from '@/components/layout/PageHeader'
import { Badge } from '@/components/ui/badge'
import { buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { joinStatusLabel, joinStatusVariant } from './labels'
import { PeopleNav } from './PeopleNav'
import { personQueries } from './queries'

export interface PeopleSearch {
  site?: string
  q?: string
  join?: string
}

export function PeoplePage({ search }: { search: PeopleSearch }) {
  const { org, can } = useSession()
  const { t, i18n } = useLingui()
  const navigate = useNavigate()
  const pages = useCursorPages()
  const orgId = org?.id ?? ''
  const sites = org?.sites ?? []
  const query = useQuery({
    ...personQueries.list(orgId, {
      siteId: search.site,
      q: search.q,
      joinStatus: search.join,
      cursor: pages.cursor,
    }),
    enabled: !!org,
  })
  const setFilter = (patch: Partial<PeopleSearch>) => {
    pages.reset()
    void navigate({
      to: '.',
      search: ((prev: PeopleSearch) => ({ ...prev, ...patch })) as never,
      replace: true,
    })
  }
  const filtered = !!(search.q || search.join || search.site)

  const columns: ColumnDef<Schema<'Person'>, unknown>[] = [
    {
      header: t`Name`,
      cell: ({ row }) => (
        <Link
          to="/orgs/$orgId/people/$personId"
          params={{ orgId, personId: row.original.id }}
          className="font-medium underline-offset-4 hover:underline"
        >
          {row.original.display_name}
        </Link>
      ),
    },
    {
      header: t`Phone`,
      cell: ({ row }) => <span dir="ltr">{row.original.contact.phone ?? '–'}</span>,
    },
    { header: t`Email`, cell: ({ row }) => row.original.contact.email ?? '–' },
    {
      header: t`Account`,
      cell: ({ row }) => (
        <Badge variant={joinStatusVariant(row.original.join_status)}>
          {joinStatusLabel(row.original.join_status, i18n)}
        </Badge>
      ),
    },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={<Trans>People</Trans>}
        actions={
          can('people.manage') ? (
            <Link to="/orgs/$orgId/people/new" params={{ orgId }} className={buttonVariants()}>
              <PlusIcon aria-hidden="true" />
              <Trans>Add a person</Trans>
            </Link>
          ) : null
        }
      />
      <PeopleNav />
      <div className="flex flex-wrap items-end gap-3">
        <Input
          type="search"
          className="w-56"
          aria-label={t`Search people`}
          placeholder={t`Search name, phone or email`}
          defaultValue={search.q ?? ''}
          onChange={(e) => setFilter({ q: e.target.value || undefined })}
        />
        {sites.length > 1 ? (
          <Select
            aria-label={t`Site`}
            className="w-auto"
            value={search.site ?? ''}
            onChange={(e) => setFilter({ site: e.target.value || undefined })}
          >
            <option value="">{t`All sites`}</option>
            {sites.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        ) : null}
        <Select
          aria-label={t`Account status`}
          className="w-auto"
          value={search.join ?? ''}
          onChange={(e) => setFilter({ join: e.target.value || undefined })}
        >
          <option value="">{t`Any account status`}</option>
          <option value="joined">{t`Joined`}</option>
          <option value="invited">{t`Invited`}</option>
          <option value="no_account">{t`No account`}</option>
        </Select>
      </div>
      <DataTable
        caption={t`People`}
        columns={columns}
        data={query.data?.items}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => void query.refetch()}
        getRowId={(p) => p.id}
        pager={{
          page: pages.page,
          hasPrev: pages.hasPrev,
          hasNext: !!query.data?.next_cursor,
          onPrev: pages.prev,
          onNext: () => query.data?.next_cursor && pages.next(query.data.next_cursor),
        }}
        empty={
          <ListEmpty
            filtered={filtered}
            icon={UsersIcon}
            title={<Trans>No people yet</Trans>}
            description={
              <Trans>Add people one by one or import them with the import wizard.</Trans>
            }
          />
        }
      />
    </div>
  )
}
