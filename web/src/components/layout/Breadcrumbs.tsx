import { Link } from '@tanstack/react-router'
import { ChevronRightIcon } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'

export interface Crumb {
  label: React.ReactNode
  /** Path template with `$orgId`; omit for the current page. */
  to?: string
  params?: Record<string, string>
  search?: Record<string, unknown>
}

/** Breadcrumb trail (web plan §10); the last crumb is the current page. */
export function Breadcrumbs({ items, orgId }: { items: Crumb[]; orgId: string }) {
  const { t } = useLingui()
  return (
    <nav aria-label={t`Breadcrumb`} className="text-sm text-muted-foreground">
      <ol className="flex flex-wrap items-center gap-1">
        {items.map((c, i) => {
          const last = i === items.length - 1
          return (
            <li key={i} className="flex items-center gap-1">
              {c.to && !last ? (
                <Link
                  to={c.to as '/orgs/$orgId'}
                  params={{ orgId, ...c.params } as { orgId: string }}
                  search={c.search as never}
                  className="rounded-sm underline-offset-4 hover:underline focus-visible:ring-[3px] focus-visible:ring-ring focus-visible:outline-none"
                >
                  {c.label}
                </Link>
              ) : (
                <span
                  aria-current={last ? 'page' : undefined}
                  className={last ? 'text-foreground' : undefined}
                >
                  {c.label}
                </span>
              )}
              {last ? null : <ChevronRightIcon className="rtl-flip size-3.5" aria-hidden="true" />}
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
