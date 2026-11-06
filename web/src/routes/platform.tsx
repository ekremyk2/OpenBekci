import { createFileRoute, Link } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { ServerIcon } from 'lucide-react'
import { ForbiddenError, requireSession } from '@/app/guards'
import { EmptyState } from '@/components/data/EmptyState'
import { buttonVariants } from '@/components/ui/button'

// Platform administration (organizations, health, backup status) lands with the platform endpoints.
export const Route = createFileRoute('/platform')({
  beforeLoad: async ({ context, location }) => {
    const me = await requireSession(context, location)
    if (!me.user.is_platform_admin) throw new ForbiddenError()
  },
  component: () => (
    <div className="mx-auto flex min-h-dvh max-w-xl items-center p-4">
      <EmptyState
        className="w-full"
        icon={ServerIcon}
        title={<Trans>Platform administration</Trans>}
        description={<Trans>Organizations, health and backup status will appear here.</Trans>}
        action={
          <Link to="/" className={buttonVariants({ variant: 'outline' })}>
            <Trans>Go home</Trans>
          </Link>
        }
      />
    </div>
  ),
})
