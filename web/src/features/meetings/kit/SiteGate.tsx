import { useLingui } from '@lingui/react/macro'
import { EmptyState } from '@/components/data/EmptyState'
import { useSiteCtx } from './api'
import { Gate } from './ui'

/** Permission gate that also needs a site to work on (these screens are per site). */
export function SiteGate({ anyOf, children }: { anyOf: string[]; children: React.ReactNode }) {
  const { t } = useLingui()
  const ctx = useSiteCtx()
  return (
    <Gate anyOf={anyOf}>
      {ctx ? (
        children
      ) : (
        <EmptyState
          title={t`No site yet`}
          description={t`Set up a site first; this area works per site.`}
        />
      )}
    </Gate>
  )
}
