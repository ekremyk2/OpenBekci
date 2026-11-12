import { Trans } from '@lingui/react/macro'
import { AccessDenied } from '@/components/data/AccessDenied'

/** Shown inside the settings area when the signed-in person lacks the permission of a tab. */
export function NoAccess() {
  return (
    <AccessDenied
      heading="h2"
      description={<Trans>Ask your organization admin if you think you should.</Trans>}
    />
  )
}
