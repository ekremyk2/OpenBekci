import { useState } from 'react'
import { flexRender, getCoreRowModel, useReactTable, type ColumnDef } from '@tanstack/react-table'
import { ChevronLeftIcon, ChevronRightIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { EmptyState } from './EmptyState'
import { ErrorState } from './ErrorState'
import { TableSkeleton } from './Skeletons'

/**
 * Server-paginated cursor state: a stack of the cursors visited so "previous" works without the
 * server supporting backward paging. `reset()` returns to the first page (call when filters change).
 */
export function useCursorPages() {
  const [stack, setStack] = useState<string[]>([])
  const cursor = stack.length > 0 ? stack[stack.length - 1] : undefined
  return {
    cursor,
    page: stack.length + 1,
    hasPrev: stack.length > 0,
    next: (nextCursor: string) => setStack((s) => [...s, nextCursor]),
    prev: () => setStack((s) => s.slice(0, -1)),
    reset: () => setStack([]),
  }
}

export interface DataTableProps<T> {
  columns: ColumnDef<T, unknown>[]
  data: T[] | undefined
  isLoading?: boolean
  error?: unknown
  onRetry?: () => void
  /** Shown when loaded and empty; `emptyFiltered` when filters are active. */
  empty: React.ReactNode
  getRowId?: (row: T) => string
  caption: string
  pager?: {
    page: number
    hasPrev: boolean
    hasNext: boolean
    onPrev: () => void
    onNext: () => void
  }
}

/**
 * TanStack Table over a server page. Four states (loading skeleton, error / no permission, empty,
 * rows) plus Previous/Next cursor controls (web plan §4: every list has all four).
 */
export function DataTable<T>({
  columns,
  data,
  isLoading,
  error,
  onRetry,
  empty,
  getRowId,
  caption,
  pager,
}: DataTableProps<T>) {
  // eslint-disable-next-line react-hooks/incompatible-library -- TanStack Table is not memoizable; the table instance is used only inside this component
  const table = useReactTable({
    data: data ?? [],
    columns,
    getCoreRowModel: getCoreRowModel(),
    getRowId,
  })

  if (error)
    return (
      <ErrorState
        error={error}
        onRetry={error instanceof AppError && error.isForbidden ? undefined : onRetry}
      />
    )
  if (isLoading) return <TableSkeleton columns={columns.length} />
  if (!data || data.length === 0) {
    return pager?.hasPrev ? (
      <div className="flex flex-col gap-3">
        {empty}
        <Pager pager={pager} />
      </div>
    ) : (
      <>{empty}</>
    )
  }
  return (
    <div className="flex flex-col gap-3">
      <div className="rounded-md border">
        <Table aria-label={caption}>
          <caption className="sr-only">{caption}</caption>
          <TableHeader>
            {table.getHeaderGroups().map((hg) => (
              <TableRow key={hg.id}>
                {hg.headers.map((h) => (
                  <TableHead key={h.id} scope="col">
                    {h.isPlaceholder ? null : flexRender(h.column.columnDef.header, h.getContext())}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.map((row) => (
              <TableRow key={row.id}>
                {row.getVisibleCells().map((cell) => (
                  <TableCell key={cell.id}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </TableCell>
                ))}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {pager ? <Pager pager={pager} /> : null}
    </div>
  )
}

function Pager({ pager }: { pager: NonNullable<DataTableProps<unknown>['pager']> }) {
  const { t } = useLingui()
  if (!pager.hasPrev && !pager.hasNext) return null
  return (
    <nav className="flex items-center justify-between gap-2" aria-label={t`Pagination`}>
      <Button variant="outline" size="sm" disabled={!pager.hasPrev} onClick={pager.onPrev}>
        <ChevronLeftIcon className="rtl-flip" aria-hidden="true" />
        <Trans>Previous</Trans>
      </Button>
      <span className="text-sm text-muted-foreground" aria-live="polite">
        <Trans>Page {pager.page}</Trans>
      </span>
      <Button variant="outline" size="sm" disabled={!pager.hasNext} onClick={pager.onNext}>
        <Trans>Next</Trans>
        <ChevronRightIcon className="rtl-flip" aria-hidden="true" />
      </Button>
    </nav>
  )
}

/** Shared "nothing here" content for filtered lists vs. genuinely empty ones. */
export function ListEmpty({
  filtered,
  icon,
  title,
  description,
  action,
}: {
  filtered: boolean
  icon?: React.ComponentProps<typeof EmptyState>['icon']
  title: React.ReactNode
  description?: React.ReactNode
  action?: React.ReactNode
}) {
  return filtered ? (
    <EmptyState
      icon={icon}
      title={<Trans>No results</Trans>}
      description={<Trans>Nothing matches these filters. Try removing some.</Trans>}
    />
  ) : (
    <EmptyState icon={icon} title={title} description={description} action={action} />
  )
}
