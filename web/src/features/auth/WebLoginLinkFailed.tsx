import { Link } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

/** Shown when a web-login link could not be exchanged (used, expired, or its app session ended). */
export function WebLoginLinkFailed() {
  return (
    <div className="flex min-h-dvh items-center justify-center p-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="text-xl">
            <Trans>This link no longer works</Trans>
          </CardTitle>
          <CardDescription>
            <Trans>
              The link was already used, has expired, or the app was signed out. Open it again from
              the app, or sign in here.
            </Trans>
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Link to="/login" search={{}} className={buttonVariants({ variant: 'default' })}>
            <Trans>Go to sign in</Trans>
          </Link>
        </CardContent>
      </Card>
    </div>
  )
}
