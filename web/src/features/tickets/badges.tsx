import { Trans } from '@lingui/react/macro'
import { LockIcon, UsersIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'

/** Open enum: an unknown semantic renders as a neutral badge with the status name. */
export function StatusBadge({ name, semantic }: { name: string; semantic: string }) {
  return (
    <Badge
      variant={
        semantic === 'closed_like' ? 'success' : semantic === 'open_like' ? 'info' : 'outline'
      }
    >
      {name}
    </Badge>
  )
}

export function VisibilityBadge({ visibility }: { visibility: string }) {
  if (visibility === 'common') {
    return (
      <Badge variant="outline">
        <UsersIcon aria-hidden="true" />
        <Trans>Common</Trans>
      </Badge>
    )
  }
  if (visibility === 'private') {
    return (
      <Badge variant="outline">
        <LockIcon aria-hidden="true" />
        <Trans>Private</Trans>
      </Badge>
    )
  }
  return <Badge variant="outline">{visibility}</Badge>
}
