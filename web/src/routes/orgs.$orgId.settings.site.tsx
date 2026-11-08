import { createFileRoute } from '@tanstack/react-router'
import { SiteSettingsLinks } from '@/features/settings/SiteSettingsLinks'

export const Route = createFileRoute('/orgs/$orgId/settings/site')({
  component: SiteSettingsLinks,
})
