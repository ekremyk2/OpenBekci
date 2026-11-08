import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { ImportWizardPage } from '@/features/import/ImportWizardPage'
import type { ImportKind } from '@/features/import/machine'

export const Route = createFileRoute('/orgs/$orgId/import')({
  validateSearch: (s: Record<string, unknown>): { kind?: ImportKind; site?: string } => ({
    kind:
      s.kind === 'units' || s.kind === 'people' || s.kind === 'opening_balances'
        ? s.kind
        : undefined,
    site: typeof s.site === 'string' && s.site ? s.site : undefined,
  }),
  component: function ImportRoute() {
    const { can } = useSession()
    const search = Route.useSearch()
    if (!can('units.import') && !can('money.post')) return <ForbiddenPage />
    return <ImportWizardPage search={search} />
  },
})
