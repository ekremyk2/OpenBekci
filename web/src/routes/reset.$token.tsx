import { createFileRoute } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { PlaceholderPage } from '@/features/auth/PlaceholderPage'

export const Route = createFileRoute('/reset/$token')({
  component: () => (
    <PlaceholderPage title={<Trans>Reset your password</Trans>}>
      <p>
        <Trans>You will be able to choose a new password here.</Trans>
      </p>
    </PlaceholderPage>
  ),
})
