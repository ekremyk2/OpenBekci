import { Link, useCanGoBack, useRouter } from '@tanstack/react-router'
import { ArrowLeftIcon, FileQuestionIcon } from 'lucide-react'
import { Trans } from '@lingui/react/macro'
import { ShellOrMain } from '@/components/layout/ShellOrMain'
import { Button, buttonVariants } from '@/components/ui/button'
import { AccessDenied } from './AccessDenied'
import { EmptyState } from './EmptyState'

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <ShellOrMain>
      <div className="mx-auto flex min-h-[60dvh] max-w-lg items-center justify-center p-6">
        {children}
      </div>
    </ShellOrMain>
  )
}

/** Back in history when there is some, else home. */
function BackOrHome() {
  const router = useRouter()
  const canGoBack = useCanGoBack()
  return canGoBack ? (
    <Button variant="outline" onClick={() => router.history.back()}>
      <ArrowLeftIcon className="rtl-flip" aria-hidden="true" />
      <Trans>Back</Trans>
    </Button>
  ) : (
    <Link to="/" className={buttonVariants({ variant: 'outline' })}>
      <Trans>Go home</Trans>
    </Link>
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
        action={<BackOrHome />}
      />
    </Centered>
  )
}

export function ForbiddenPage() {
  return (
    <Centered>
      <div className="flex flex-col items-center gap-3">
        <AccessDenied />
        <BackOrHome />
      </div>
    </Centered>
  )
}
