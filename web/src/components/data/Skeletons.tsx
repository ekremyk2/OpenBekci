import { Skeleton } from '@/components/ui/skeleton'

/** Page-level loading pattern: title line plus a few content blocks. */
export function PageSkeleton() {
  return (
    <div role="status" aria-busy="true" className="flex flex-col gap-4 p-1">
      <Skeleton className="h-8 w-1/3" />
      <Skeleton className="h-24 w-full" />
      <Skeleton className="h-24 w-full" />
    </div>
  )
}

/** Table loading pattern: header plus `rows` placeholder rows. */
export function TableSkeleton({ rows = 5, columns = 4 }: { rows?: number; columns?: number }) {
  return (
    <div role="status" aria-busy="true" className="flex flex-col gap-2">
      <Skeleton className="h-9 w-full" />
      {Array.from({ length: rows }, (_, r) => (
        <div key={r} className="flex gap-2">
          {Array.from({ length: columns }, (_, c) => (
            <Skeleton key={c} className="h-8 flex-1" />
          ))}
        </div>
      ))}
    </div>
  )
}
