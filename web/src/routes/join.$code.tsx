import { createFileRoute } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { PlaceholderPage } from '@/features/auth/PlaceholderPage'

export const Route = createFileRoute('/join/$code')({
  component: () => (
    <PlaceholderPage title={<Trans>Join your building</Trans>}>
      <p>
        <Trans>You will be able to claim your unit here. Your manager approves the request.</Trans>
      </p>
    </PlaceholderPage>
  ),
})
