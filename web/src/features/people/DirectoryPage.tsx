import { Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { ContactIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { Schema } from '@/api/draft'
import { useSession } from '@/app/session'
import { DataTable, ListEmpty, useCursorPages } from '@/components/data/DataTable'
import { SitePicker } from '@/components/forms/SitePicker'
import { PageHeader } from '@/components/layout/PageHeader'
import { Input } from '@/components/ui/input'
import { occupancyKindLabel } from '@/lib/labels'
import { useActiveSite } from '@/features/sites/useActiveSite'
import { PeopleNav } from './PeopleNav'
import { personQueries } from './queries'

/**
 * Resident directory (S8). The server filters by each person's consent for residents; managers and
 * the board always get everyone, which the banner says so nobody is surprised.
 */
export function DirectoryPage({ search }: { search: { site?: string; q?: string } }) {
  const { org, can } = useSession()
  const { t, i18n } = useLingui()
  const navigate = useNavigate()
  const pages = useCursorPages()
  const orgId = org?.id ?? ''
  const { siteId, sites, setSite } = useActiveSite(search.site)
  const query = useQuery({
    ...personQueries.directory(orgId, { siteId: siteId ?? '', q: search.q, cursor: pages.cursor }),
    enabled: !!org && !!siteId,
  })
  const manager = can('people.read')

  const columns: ColumnDef<Schema<'DirectoryEntry'>, unknown>[] = [
    {
      header: t`Name`,
      cell: ({ row }) =>
        manager ? (
          <Link
            to="/orgs/$orgId/people/$personId"
            params={{ orgId, personId: row.original.person_id }}
            className="font-medium underline-offset-4 hover:underline"
          >
            {row.original.display_name}
          </Link>
        ) : (
          <span className="font-medium">{row.original.display_name}</span>
        ),
    },
    {
      header: t`Units`,
      cell: ({ row }) =>
        row.original.units
          .map((u) => `${u.door_number} (${occupancyKindLabel(u.kind, i18n)})`)
          .join(', '),
    },
    { header: t`Phone`, cell: ({ row }) => <span dir="ltr">{row.original.phone ?? '–'}</span> },
    { header: t`Email`, cell: ({ row }) => row.original.email ?? '–' },
  ]
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={<Trans>Directory</Trans>} />
      <PeopleNav />
      <p className="text-sm text-muted-foreground">
        {manager ? (
          <Trans>
            You see everyone as a manager. Residents only see the people who chose to be listed.
          </Trans>
        ) : (
          <Trans>These neighbours chose to be listed.</Trans>
        )}
      </p>
      <div className="flex flex-wrap items-end gap-3">
        <SitePicker sites={sites} value={siteId} onChange={setSite} />
        <Input
          type="search"
          className="w-56"
          aria-label={t`Search the directory`}
          placeholder={t`Search by name`}
          defaultValue={search.q ?? ''}
          onChange={(e) => {
            pages.reset()
            void navigate({
              to: '.',
              search: ((p: object) => ({ ...p, q: e.target.value || undefined })) as never,
              replace: true,
            })
          }}
        />
      </div>
      <DataTable
        caption={t`Directory`}
        columns={columns}
        data={query.data?.items}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => void query.refetch()}
        getRowId={(e) => e.person_id}
        pager={{
          page: pages.page,
          hasPrev: pages.hasPrev,
          hasNext: !!query.data?.next_cursor,
          onPrev: pages.prev,
          onNext: () => query.data?.next_cursor && pages.next(query.data.next_cursor),
        }}
        empty={
          <ListEmpty
            filtered={!!search.q}
            icon={ContactIcon}
            title={<Trans>The directory is empty</Trans>}
            description={
              <Trans>
                Nobody is listed yet. The directory must be switched on in the site settings and
                people choose to appear in it.
              </Trans>
            }
          />
        }
      />
    </div>
  )
}
