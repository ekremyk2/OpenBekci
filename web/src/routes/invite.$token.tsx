import { createFileRoute } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { PlaceholderPage } from '@/features/auth/PlaceholderPage'

export const Route = createFileRoute('/invite/$token')({
  component: () => (
    <PlaceholderPage title={<Trans>Accept your invitation</Trans>}>
      <p>
        <Trans>You will be able to create or link your account here.</Trans>
      </p>
    </PlaceholderPage>
  ),
})
