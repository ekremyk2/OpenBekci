import { createFileRoute, Link } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { buttonVariants } from '@/components/ui/button'
import { AuthShell } from '@/features/auth/AuthShell'

// The server has no password-reset operation: signing in by an e-mailed link is the way back in.
export const Route = createFileRoute('/reset/$token')({
  component: () => (
    <AuthShell
      title={<Trans>Forgot your password?</Trans>}
      description={
        <Trans>
          This server does not reset passwords. Ask for a sign-in link by e-mail instead; it signs
          you in once.
        </Trans>
      }
    >
      <Link
        to="/login"
        search={{ method: 'magic_link' }}
        className={buttonVariants({ variant: 'default' })}
      >
        <Trans>Sign in with an e-mail link</Trans>
      </Link>
    </AuthShell>
  ),
})
