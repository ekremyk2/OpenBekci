import { useMemo } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { DownloadIcon, HomeIcon, PlusIcon, UploadIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { Schema } from '@/api/draft'
import { useSession } from '@/app/session'
import { DataTable, ListEmpty, useCursorPages } from '@/components/data/DataTable'
import { SitePicker } from '@/components/forms/SitePicker'
import { PageHeader } from '@/components/layout/PageHeader'
import { SectionNav } from '@/components/layout/SectionNav'
import { Badge } from '@/components/ui/badge'
import { buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { useActiveSite } from '@/features/sites/useActiveSite'
import { siteQueries } from '@/features/sites/queries'
import { unitTypeLabel } from './labels'
import { UNIT_TYPES, unitQueries } from './queries'

export interface UnitsSearch {
  site?: string
  block?: string
  type?: string
  vacant?: 'yes' | 'no'
  q?: string
}

export function SetupNav() {
  const { t } = useLingui()
  return (
    <SectionNav
      label={t`Setup`}
      links={[
        { id: 'units', label: t`Units`, to: '/orgs/$orgId/units', permission: 'units.read' },
        {
          id: 'keys',
          label: t`Allocation keys`,
          to: '/orgs/$orgId/allocation-keys',
          permission: 'units.read',
        },
        { id: 'import', label: t`Import`, to: '/orgs/$orgId/import', permission: 'units.import' },
      ]}
    />
  )
}

/** Units of one site: server-paginated table with block, type, vacancy and text filters in the URL. */
export function UnitsPage({ search }: { search: UnitsSearch }) {
  const { org, can } = useSession()
  const { t, i18n } = useLingui()
  const navigate = useNavigate()
  const { siteId, sites, setSite } = useActiveSite(search.site)
  const pages = useCursorPages()
  const orgId = org?.id ?? ''
  const vacant = search.vacant === 'yes' ? true : search.vacant === 'no' ? false : undefined
  const filters = {
    siteId: siteId ?? '',
    blockId: search.block,
    type: search.type,
    vacant,
    q: search.q,
    cursor: pages.cursor,
  }
  const query = useQuery({ ...unitQueries.list(orgId, filters), enabled: !!org && !!siteId })
  const blocksQ = useQuery({
    ...siteQueries.blocks(orgId, siteId ?? ''),
    enabled: !!org && !!siteId,
  })
  const blocks = blocksQ.data?.items ?? []
  const singleBlock = blocks.length <= 1
  const blockName = (id: string) => blocks.find((b) => b.id === id)?.name ?? ''
  const filtered = !!(search.block || search.type || search.vacant || search.q)

  const setFilter = (patch: Partial<UnitsSearch>) => {
    pages.reset()
    void navigate({
      to: '.',
      search: ((prev: UnitsSearch) => ({ ...prev, ...patch })) as never,
      replace: true,
    })
  }

  // appurtenances are shown under their flat when both are on the page
  const rows = useMemo(() => {
    const items = query.data?.items ?? []
    const ids = new Set(items.map((u) => u.id))
    const top = items.filter((u) => !u.parent_unit_id || !ids.has(u.parent_unit_id))
    return top.flatMap((u) => [u, ...items.filter((c) => c.parent_unit_id === u.id)])
  }, [query.data])

  const columns: ColumnDef<Schema<'Unit'>, unknown>[] = [
    {
      header: t`Door`,
      cell: ({ row }) => {
        const u = row.original
        return (
          <span className={u.parent_unit_id ? 'ps-6' : undefined}>
            <Link
              to="/orgs/$orgId/units/$unitId"
              params={{ orgId, unitId: u.id }}
              className="font-medium underline-offset-4 hover:underline"
            >
              {u.door_number}
            </Link>
          </span>
        )
      },
    },
    ...(singleBlock
      ? []
      : [
          {
            header: t`Block`,
            cell: ({ row }: { row: { original: Schema<'Unit'> } }) =>
              blockName(row.original.block_id),
          } as ColumnDef<Schema<'Unit'>, unknown>,
        ]),
    { header: t`Type`, cell: ({ row }) => unitTypeLabel(row.original.type, i18n) },
    {
      header: t`Area`,
      cell: ({ row }) => (row.original.area_m2 == null ? '–' : `${row.original.area_m2} m²`),
    },
    {
      header: t`Occupancy`,
      cell: ({ row }) =>
        row.original.parent_unit_id ? (
          <Badge variant="outline">
            <Trans>Appurtenance</Trans>
          </Badge>
        ) : row.original.vacant ? (
          <Badge variant="warning">
            <Trans>Vacant</Trans>
          </Badge>
        ) : (
          <Badge variant="success">
            <Trans>Occupied</Trans>
          </Badge>
        ),
    },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={<Trans>Units</Trans>}
        actions={
          <>
            {can('vehicles.read') && siteId ? (
              <a
                className={buttonVariants({ variant: 'outline' })}
                href={`/v1/orgs/${orgId}/vehicles:export?site_id=${siteId}&format=csv`}
                download
              >
                <DownloadIcon aria-hidden="true" />
                <Trans>Plates for the camera operator (CSV)</Trans>
              </a>
            ) : null}
            {can('units.import') ? (
              <Link
                to="/orgs/$orgId/import"
                params={{ orgId }}
                search={{ kind: 'units', site: siteId }}
                className={buttonVariants({ variant: 'outline' })}
              >
                <UploadIcon aria-hidden="true" />
                <Trans>Import units</Trans>
              </Link>
            ) : null}
            {can('units.manage') ? (
              <Link
                to="/orgs/$orgId/units/new"
                params={{ orgId }}
                search={{ site: siteId }}
                className={buttonVariants()}
              >
                <PlusIcon aria-hidden="true" />
                <Trans>New unit</Trans>
              </Link>
            ) : null}
          </>
        }
      />
      <SetupNav />
      <div className="flex flex-wrap items-end gap-3">
        <SitePicker sites={sites} value={siteId} onChange={setSite} />
        <Input
          type="search"
          className="w-48"
          aria-label={t`Search units`}
          placeholder={t`Search door or title deed no.`}
          defaultValue={search.q ?? ''}
          onChange={(e) => setFilter({ q: e.target.value || undefined })}
        />
        {singleBlock ? null : (
          <Select
            aria-label={t`Block`}
            className="w-auto"
            value={search.block ?? ''}
            onChange={(e) => setFilter({ block: e.target.value || undefined })}
          >
            <option value="">{t`All blocks`}</option>
            {blocks.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </Select>
        )}
        <Select
          aria-label={t`Type`}
          className="w-auto"
          value={search.type ?? ''}
          onChange={(e) => setFilter({ type: e.target.value || undefined })}
        >
          <option value="">{t`All types`}</option>
          {UNIT_TYPES.map((x) => (
            <option key={x} value={x}>
              {unitTypeLabel(x, i18n)}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t`Occupancy`}
          className="w-auto"
          value={search.vacant ?? ''}
          onChange={(e) =>
            setFilter({ vacant: (e.target.value || undefined) as UnitsSearch['vacant'] })
          }
        >
          <option value="">{t`Vacant and occupied`}</option>
          <option value="yes">{t`Vacant only`}</option>
          <option value="no">{t`Occupied only`}</option>
        </Select>
      </div>
      <DataTable
        caption={t`Units`}
        columns={columns}
        data={siteId ? rows : []}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => void query.refetch()}
        getRowId={(u) => u.id}
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
            icon={HomeIcon}
            title={<Trans>No units yet</Trans>}
            description={<Trans>Add units one by one or import them from a spreadsheet.</Trans>}
          />
        }
      />
    </div>
  )
}
