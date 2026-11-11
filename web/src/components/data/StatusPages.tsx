import { Link } from '@tanstack/react-router'
import { FileQuestionIcon, LockIcon } from 'lucide-react'
import { Trans } from '@lingui/react/macro'
import { buttonVariants } from '@/components/ui/button'
import { EmptyState } from './EmptyState'

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="mx-auto flex min-h-[60dvh] max-w-lg items-center justify-center p-6">
      {children}
    </div>
  )
}

export function NotFoundPage() {
  return (
    <Centered>
      <EmptyState
        heading="h1"
        icon={FileQuestionIcon}
        title={<Trans>Page not found</Trans>}
        description={<Trans>The page you are looking for does not exist or has moved.</Trans>}
        action={
          <Link to="/" className={buttonVariants({ variant: 'outline' })}>
            <Trans>Go home</Trans>
          </Link>
        }
      />
    </Centered>
  )
}

export function ForbiddenPage() {
  return (
    <Centered>
      <EmptyState
        heading="h1"
        icon={LockIcon}
        title={<Trans>You do not have access to this page</Trans>}
        description={<Trans>Ask your manager if you think you should.</Trans>}
        action={
          <Link to="/" className={buttonVariants({ variant: 'outline' })}>
            <Trans>Go home</Trans>
          </Link>
        }
      />
    </Centered>
  )
}
