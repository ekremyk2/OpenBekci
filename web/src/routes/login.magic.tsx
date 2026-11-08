import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { Trans } from '@lingui/react/macro'
import { PlaceholderPage } from '@/features/auth/PlaceholderPage'

// Magic-link landing (backend §6). Redeeming the token needs the auth endpoints of a later backend task.
export const Route = createFileRoute('/login/magic')({
  validateSearch: z.object({ token: z.string().optional().catch(undefined) }),
  component: () => (
    <PlaceholderPage title={<Trans>Signing you in</Trans>}>
      <p>
        <Trans>Your sign-in link will be completed here.</Trans>
      </p>
    </PlaceholderPage>
  ),
})
