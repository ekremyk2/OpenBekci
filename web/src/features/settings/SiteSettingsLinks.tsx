import { Link } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { SiteChatGroupCards } from '@/features/chat/TelegramGroupCard'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

/**
 * Site settings (timezone, currency, fiscal year, lock date, bank details, late-fee policy) belong
 * to the setup screens, which live on another branch. This page only links to their route
 * (`/orgs/$orgId/sites/$siteId`) and to the settings that live here.
 */
export function SiteSettingsLinks() {
  const { org, can } = useSession()
  const orgId = org?.id ?? ''
  return (
    <div className="flex max-w-xl flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Site settings</Trans>
          </CardTitle>
          <CardDescription>
            <Trans>Timezone, currency, fiscal year, bank details and late-fee policy.</Trans>
          </CardDescription>
        </CardHeader>
        <CardContent>
          <ul className="flex flex-col gap-2">
            {org?.sites.map((site) => (
              <li key={site.id}>
                <Link
                  // Route owned by the setup screens; typed loosely until that branch merges.
                  to={'/orgs/$orgId/sites/$siteId' as '/orgs/$orgId'}
                  params={{ orgId, siteId: site.id } as { orgId: string }}
                  className="text-primary underline-offset-4 hover:underline"
                >
                  {site.name}
                </Link>
              </li>
            ))}
          </ul>
        </CardContent>
      </Card>
      {can('chatgroups.manage')
        ? org?.sites.map((site) => (
            <SiteChatGroupCards key={site.id} siteId={site.id} siteName={site.name} />
          ))
        : null}
      {can('tickets.manage') || can('notifications.manage') ? (
        <Card>
          <CardHeader>
            <CardTitle>
              <Trans>Operations</Trans>
            </CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-2 text-sm">
            {can('tickets.manage') ? (
              <Link
                to="/orgs/$orgId/settings/tickets"
                params={{ orgId }}
                className="text-primary underline-offset-4 hover:underline"
              >
                <Trans>Ticket workflow and categories</Trans>
              </Link>
            ) : null}
            {can('notifications.manage') ? (
              <Link
                to="/orgs/$orgId/settings/notifications"
                params={{ orgId }}
                className="text-primary underline-offset-4 hover:underline"
              >
                <Trans>Notification defaults</Trans>
              </Link>
            ) : null}
          </CardContent>
        </Card>
      ) : null}
    </div>
  )
}
