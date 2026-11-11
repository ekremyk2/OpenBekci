import type { LucideIcon } from 'lucide-react'
import { InboxIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

export function EmptyState({
  icon: Icon = InboxIcon,
  title,
  description,
  action,
  className,
  heading: Heading = 'h2',
}: {
  icon?: LucideIcon
  title: React.ReactNode
  description?: React.ReactNode
  action?: React.ReactNode
  className?: string
  /** h1 when the empty state is the whole page */
  heading?: 'h1' | 'h2'
}) {
  return (
    <div
      className={cn(
        'flex flex-col items-center gap-3 rounded-lg border border-dashed p-10 text-center',
        className,
      )}
    >
      <Icon className="size-8 text-muted-foreground" aria-hidden="true" />
      <Heading className="text-base font-semibold">{title}</Heading>
      {description ? (
        <p className="max-w-prose text-sm text-muted-foreground">{description}</p>
      ) : null}
      {action}
    </div>
  )
}
