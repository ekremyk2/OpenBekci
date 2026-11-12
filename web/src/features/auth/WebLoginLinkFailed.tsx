import { Link } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { buttonVariants } from '@/components/ui/button'
import { AuthShell } from './AuthShell'

/** Shown when a sign-in link could not be exchanged (used, expired, or its app session ended). */
export function WebLoginLinkFailed({ magic }: { magic?: boolean }) {
  return (
    <AuthShell
      title={<Trans>This link no longer works</Trans>}
      description={
        magic ? (
          <Trans>
            The link was already used or has expired. Ask for a new one on the sign-in page.
          </Trans>
        ) : (
          <Trans>
            The link was already used, has expired, or the app was signed out. Open it again from
            the app, or sign in here.
          </Trans>
        )
      }
    >
      <Link to="/login" search={{}} className={buttonVariants({ variant: 'default' })}>
        <Trans>Go to sign in</Trans>
      </Link>
    </AuthShell>
  )
}
