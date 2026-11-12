import { useCanGoBack, useMatches, useNavigate, useParams, useRouter } from '@tanstack/react-router'
import { ArrowLeftIcon } from 'lucide-react'
import { Trans } from '@lingui/react/macro'
import { Button } from '@/components/ui/button'

const ORG = '/orgs/$orgId/'
/** Detail and form pages (no sidebar entry of their own): the last route segment gives them away. */
const SUBPAGE =
  /(^|\/)(\$[A-Za-z]+|new|edit|desk|qr|return|invitations|decisions|cash-boxes)$|^meetings\/settings$/
/** Parent route when "strip the last segment" is not a route of its own. */
const PARENT: Record<string, string> = {
  'documents/f': 'documents',
  'move-out': 'people',
  pay: 'money',
}

/** Where a sub-page's Back goes when there is no in-app history (deep link, new tab). */
export function backTarget(routeId: string): string | undefined {
  if (routeId === '/platform') return '/'
  if (!routeId.startsWith(ORG)) return undefined
  const path = routeId.slice(ORG.length).replace(/\/$/, '')
  if (!SUBPAGE.test(path)) return undefined
  const parent = path.split('/').slice(0, -1).join('/')
  return ORG + (PARENT[parent] ?? parent)
}

/** One consistent Back on every detail/form page: router history, else the parent list route. */
export function BackLink() {
  const matches = useMatches()
  const router = useRouter()
  const canGoBack = useCanGoBack()
  const navigate = useNavigate()
  const params = useParams({ strict: false })
  const fallback = backTarget(matches.at(-1)?.routeId ?? '')
  if (!fallback) return null
  return (
    <Button
      variant="ghost"
      size="sm"
      className="mb-2 -ms-2 min-h-11"
      onClick={() =>
        canGoBack
          ? router.history.back()
          : void navigate({ to: fallback as '/orgs/$orgId', params: params as { orgId: string } })
      }
    >
      <ArrowLeftIcon className="rtl-flip" aria-hidden="true" />
      <Trans>Back</Trans>
    </Button>
  )
}
