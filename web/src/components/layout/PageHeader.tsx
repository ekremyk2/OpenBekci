import { useSession } from '@/app/session'
import { Breadcrumbs, type Crumb } from './Breadcrumbs'

/** Breadcrumbs, the page title (the one `h1`) and the primary actions. */
export function PageHeader({
  title,
  crumbs,
  actions,
  description,
}: {
  title: React.ReactNode
  crumbs?: Crumb[]
  actions?: React.ReactNode
  description?: React.ReactNode
}) {
  const { org } = useSession()
  return (
    <header className="flex flex-col gap-2">
      {crumbs && org ? <Breadcrumbs items={crumbs} orgId={org.id} /> : null}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold">{title}</h1>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
      {description ? (
        <p className="max-w-prose text-sm text-muted-foreground">{description}</p>
      ) : null}
    </header>
  )
}
