import { LockIcon } from 'lucide-react'
import { Trans } from '@lingui/react/macro'
import { EmptyState } from './EmptyState'

/**
 * The one "no access" state. A denied page has no `h1` of its own, so it is the `h1` by default;
 * pass `heading="h2"` where it sits inside a page that already has one.
 */
export function AccessDenied({
  heading = 'h1',
  description,
}: {
  heading?: 'h1' | 'h2'
  description?: React.ReactNode
}) {
  return (
    <EmptyState
      heading={heading}
      icon={LockIcon}
      title={<Trans>You do not have access to this page</Trans>}
      description={description ?? <Trans>Ask your manager if you think you should.</Trans>}
    />
  )
}
