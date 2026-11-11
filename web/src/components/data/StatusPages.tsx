import { Link } from '@tanstack/react-router'
import { FileQuestionIcon, LockIcon } from 'lucide-react'
import { Trans } from '@lingui/react/macro'
import { buttonVariants } from '@/components/ui/button'
import { EmptyState } from './EmptyState'

function Centered({ title, children }: { title: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="mx-auto flex min-h-dvh max-w-lg items-center justify-center p-6">
      <h1 className="sr-only">{title}</h1>
      {children}
    </div>
  )
}

export function NotFoundPage() {
  return (
    <Centered title={<Trans>Page not found</Trans>}>
      <EmptyState
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
    <Centered title={<Trans>You do not have access to this page</Trans>}>
      <EmptyState
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
