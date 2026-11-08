export const SITE_TABS = ['general', 'settings', 'bank', 'lock', 'blocks', 'transfer'] as const
export type SiteTab = (typeof SITE_TABS)[number]
