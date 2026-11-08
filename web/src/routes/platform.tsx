import { createFileRoute } from '@tanstack/react-router'
import { ForbiddenError, requireSession } from '@/app/guards'
import { PlatformPage } from '@/features/platform/PlatformPage'

// Platform administration: organizations, health, jobs, instance settings, backup status.
export const Route = createFileRoute('/platform')({
  beforeLoad: async ({ context, location }) => {
    const me = await requireSession(context, location)
    if (!me.user.is_platform_admin) throw new ForbiddenError()
  },
  component: PlatformPage,
})
