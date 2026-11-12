import { AlertTriangleIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { problemMessage } from '@/lib/labels'
import { Button } from '@/components/ui/button'
import { AccessDenied } from './AccessDenied'
import { EmptyState } from './EmptyState'

/**
 * Inline error panel. A 403 from the API becomes the "no permission" state (web plan §6);
 * everything else offers a retry and shows the request id for support.
 */
export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t, i18n } = useLingui()
  const app = error instanceof AppError ? error : undefined

  if (app?.isForbidden) return <AccessDenied heading="h2" />
  return (
    <EmptyState
      icon={AlertTriangleIcon}
      title={t`Something went wrong`}
      description={
        <>
          {(app && problemMessage(app.code, i18n)) ||
            (app?.status === 404
              ? t`We could not find that.`
              : t`The request failed. Please try again.`)}
          {app?.requestId ? (
            <span className="mt-2 block text-xs">
              <Trans>Reference: {app.requestId}</Trans>
            </span>
          ) : null}
        </>
      }
      action={
        onRetry ? (
          <Button variant="outline" onClick={onRetry}>
            <Trans>Try again</Trans>
          </Button>
        ) : undefined
      }
    />
  )
}
