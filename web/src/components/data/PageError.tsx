import { SearchXIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { ErrorState } from './ErrorState'
import { EmptyState } from './EmptyState'

/**
 * Load error of a page that opens one record by id: an id that does not exist (404, or a malformed
 * id the server rejects) is a "not found" page, not a failure to retry.
 */
export function PageError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useLingui()
  const status = error instanceof AppError ? error.status : undefined
  if (status === 404 || status === 400 || status === 422) {
    return (
      <EmptyState
        heading="h1"
        icon={SearchXIcon}
        title={t`Not found`}
        description={<Trans>This page does not exist, or it was removed.</Trans>}
      />
    )
  }
  return <ErrorState error={error} onRetry={onRetry} />
}
