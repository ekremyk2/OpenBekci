import { useLingui } from '@lingui/react/macro'
import { Badge } from '@/components/ui/badge'

/**
 * "Pack default" marker next to a site setting a pack's preset filled and the site has not changed
 * since (`pack_defaults` of the site settings, plan 04 §3.7). Editing the setting makes it the
 * site's own and the marker disappears.
 */
export function PackDefaultBadge({ pack }: { pack: string }) {
  const { t } = useLingui()
  return (
    <Badge variant="info" title={t`Filled in by the ${pack} pack; edit it to make it your own`}>
      {t`Pack default`}
    </Badge>
  )
}

export const packDefaultEntries = (
  settings: { pack_defaults?: Record<string, { pack: string; value?: unknown }> } | undefined,
  onlyPack?: string,
) =>
  Object.entries(settings?.pack_defaults ?? {})
    .filter(([, v]) => !onlyPack || v.pack === onlyPack)
    .sort(([a], [b]) => a.localeCompare(b))
