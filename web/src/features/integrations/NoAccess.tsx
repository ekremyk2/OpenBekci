import { Trans } from '@lingui/react/macro'
import { LockIcon } from 'lucide-react'
import { EmptyState } from '@/components/data/EmptyState'

/** Shown inside the settings area when the signed-in person lacks the permission of a tab. */
export function NoAccess() {
  return (
    <EmptyState
      icon={LockIcon}
      title={<Trans>You do not have access to this</Trans>}
      description={<Trans>Ask your organization admin if you think you should.</Trans>}
    />
  )
}
