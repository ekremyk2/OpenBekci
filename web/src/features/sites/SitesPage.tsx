import { Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { Building2Icon, PlusIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { Schema } from '@/api/draft'
import { useSession } from '@/app/session'
import { DataTable, ListEmpty, useCursorPages } from '@/components/data/DataTable'
import { PageHeader } from '@/components/layout/PageHeader'
import { Badge } from '@/components/ui/badge'
import { buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { siteQueries } from './queries'

export function SitesPage({ q, onSearch }: { q?: string; onSearch: (q: string) => void }) {
  const { org, can } = useSession()
  const { t } = useLingui()
  const navigate = useNavigate()
  const pages = useCursorPages()
  const orgId = org?.id ?? ''
  const query = useQuery({
    ...siteQueries.list(orgId, { q, cursor: pages.cursor }),
    enabled: !!org,
  })

  const columns: ColumnDef<Schema<'Site'>, unknown>[] = [
    {
      header: t`Name`,
      cell: ({ row }) => (
        <Link
          to="/orgs/$orgId/sites/$siteId"
          params={{ orgId, siteId: row.original.id }}
          className="font-medium underline-offset-4 hover:underline"
        >
          {row.original.name}
        </Link>
      ),
    },
    { header: t`City`, cell: ({ row }) => row.original.address.city ?? '–' },
    { header: t`Time zone`, cell: ({ row }) => row.original.timezone },
    { header: t`Currency`, cell: ({ row }) => row.original.currency },
    {
      header: t`Status`,
      cell: ({ row }) =>
        row.original.status === 'active' ? (
          <Badge variant="success">
            <Trans>Active</Trans>
          </Badge>
        ) : (
          <Badge variant="secondary">{row.original.status}</Badge>
        ),
    },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={<Trans>Sites</Trans>}
        actions={
          can('sites.manage') ? (
            <Link to="/orgs/$orgId/sites/new" params={{ orgId }} className={buttonVariants()}>
              <PlusIcon aria-hidden="true" />
              <Trans>New site</Trans>
            </Link>
          ) : null
        }
      />
      <Input
        type="search"
        className="max-w-xs"
        aria-label={t`Search sites`}
        placeholder={t`Search sites`}
        defaultValue={q ?? ''}
        onChange={(e) => {
          pages.reset()
          onSearch(e.target.value)
        }}
      />
      <DataTable
        caption={t`Sites`}
        columns={columns}
        data={query.data?.items}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => void query.refetch()}
        getRowId={(s) => s.id}
        pager={{
          page: pages.page,
          hasPrev: pages.hasPrev,
          hasNext: !!query.data?.next_cursor,
          onPrev: pages.prev,
          onNext: () => query.data?.next_cursor && pages.next(query.data.next_cursor),
        }}
        empty={
          <ListEmpty
            filtered={!!q}
            icon={Building2Icon}
            title={<Trans>No sites yet</Trans>}
            description={
              <Trans>
                A site is a building or a complex you manage. Create the first one to start.
              </Trans>
            }
            action={
              can('sites.manage') ? (
                <button
                  className={buttonVariants()}
                  onClick={() => void navigate({ to: '/orgs/$orgId/sites/new', params: { orgId } })}
                >
                  <Trans>Create a site</Trans>
                </button>
              ) : undefined
            }
          />
        }
      />
    </div>
  )
}
