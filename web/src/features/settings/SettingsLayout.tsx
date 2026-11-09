import { Link, Outlet } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { cn } from '@/lib/utils'

/** Debug-only screens (codes/labels). `VITE_DEBUG=true` enables them in a production build. */
export const DEBUG = import.meta.env.DEV || import.meta.env.VITE_DEBUG === 'true'

export function SettingsLayout() {
  const { t } = useLingui()
  const { org, can } = useSession()
  const orgId = org?.id ?? ''
  const tabs = [
    { to: '/orgs/$orgId/settings', label: t`Organization`, show: true, exact: true },
    { to: '/orgs/$orgId/settings/site', label: t`Site`, show: true },
    { to: '/orgs/$orgId/settings/tickets', label: t`Tickets`, show: can('tickets.manage') },
    {
      to: '/orgs/$orgId/settings/notifications',
      label: t`Notifications`,
      show: can('notifications.manage'),
    },
    { to: '/orgs/$orgId/settings/chat', label: t`Chat`, show: can('chat.manage') },
    { to: '/orgs/$orgId/settings/audit', label: t`Audit log`, show: can('audit.read') },
    { to: '/orgs/$orgId/settings/packs', label: t`Packs`, show: can('packs.read') },
    {
      to: '/orgs/$orgId/settings/api-keys',
      label: t`API keys`,
      show: can('apikeys.read') || can('apikeys.manage'),
    },
    {
      to: '/orgs/$orgId/settings/webhooks',
      label: t`Webhooks`,
      show: can('webhooks.read') || can('webhooks.manage'),
    },
    { to: '/orgs/$orgId/settings/codes', label: t`Codes`, show: DEBUG && can('codes.read') },
  ] as const
  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-2xl font-semibold">
        <Trans>Settings</Trans>
      </h1>
      <nav aria-label={t`Settings sections`} className="flex flex-wrap gap-1 border-b">
        {tabs
          .filter((x) => x.show)
          .map((tab) => (
            <Link
              key={tab.to}
              to={tab.to}
              params={{ orgId }}
              activeOptions={{ exact: 'exact' in tab }}
              className={cn(
                '-mb-px border-b-2 border-transparent px-3 py-2 text-sm font-medium text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring',
                '[&.active]:border-primary [&.active]:text-foreground',
              )}
            >
              {tab.label}
            </Link>
          ))}
      </nav>
      <Outlet />
    </div>
  )
}
