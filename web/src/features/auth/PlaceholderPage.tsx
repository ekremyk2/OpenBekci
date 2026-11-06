import { Link } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

/** Public landing for flows whose backend endpoints arrive in a later task. */
export function PlaceholderPage({
  title,
  children,
}: {
  title: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <div className="flex min-h-dvh items-center justify-center p-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="text-xl">{title}</CardTitle>
          <CardDescription>
            <Trans>This step is not available in this build yet.</Trans>
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4 text-sm">
          {children}
          <Link to="/login" search={{}} className={buttonVariants({ variant: 'outline' })}>
            <Trans>Back to sign in</Trans>
          </Link>
        </CardContent>
      </Card>
    </div>
  )
}
