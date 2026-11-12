import { Link } from '@tanstack/react-router'
import { useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { cn } from '@/lib/utils'
import { ScrollStrip } from './ScrollStrip'

export interface SectionLink {
  id: string
  label: React.ReactNode
  /** Path template with `$orgId`. */
  to: string
  permission?: string
  search?: Record<string, unknown>
}

/** Secondary navigation inside a feature area (for example People: persons, invitations, claims). */
export function SectionNav({ links, label }: { links: SectionLink[]; label: string }) {
  const { org, can } = useSession()
  const { t } = useLingui()
  if (!org) return null
  const visible = links.filter((l) => !l.permission || can(l.permission))
  return (
    <ScrollStrip label={label || t`Section`} className="border-b pb-2">
      {visible.map((l) => (
        <Link
          key={l.id}
          to={l.to as '/orgs/$orgId'}
          params={{ orgId: org.id }}
          search={l.search as never}
          className={cn(
            'flex min-h-11 shrink-0 items-center rounded-md px-3 py-1.5 text-sm font-medium outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background',
            '[&.active]:bg-accent [&.active]:text-accent-foreground',
          )}
        >
          {l.label}
        </Link>
      ))}
    </ScrollStrip>
  )
}
