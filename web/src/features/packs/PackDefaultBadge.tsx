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

const humanize = (k: string) => {
  const s = k.replace(/[_-]+/g, ' ').trim()
  return s.charAt(0).toUpperCase() + s.slice(1)
}

/** Readable label for a pack-default key or a field inside its value; unknown keys are humanized. */
export function useSettingLabel() {
  const { t } = useLingui()
  const known: Record<string, string> = {
    late_fee_policy: t`Late fee policy`,
    quorum: t`Meeting quorum`,
    thresholds: t`Voting thresholds`,
    monthly_rate_percent: t`Monthly rate (%)`,
    grace_days: t`Grace days`,
    first_call: t`First call`,
    second_call: t`Second call`,
  }
  return (k: string) => known[k] ?? humanize(k)
}

/** A pack default's value as labelled fields (nested objects nest); wraps, never overflows. */
export function PackValue({ value, raw }: { value: unknown; raw?: boolean }) {
  const label = useSettingLabel()
  const { t } = useLingui()
  if (raw)
    return (
      <pre
        className="max-w-full whitespace-pre-wrap break-all rounded bg-muted p-2 text-xs"
        dir="ltr"
      >
        {JSON.stringify(value, null, 2)}
      </pre>
    )
  if (value && typeof value === 'object' && !Array.isArray(value))
    return (
      <dl className="grid gap-1 text-sm">
        {Object.entries(value).map(([k, v]) => (
          <div key={k} className="flex flex-wrap gap-x-2">
            <dt className="text-muted-foreground">{label(k)}:</dt>
            <dd className="min-w-0 break-words">
              <PackValue value={v} />
            </dd>
          </div>
        ))}
      </dl>
    )
  const text = Array.isArray(value)
    ? value.map((v) => (typeof v === 'string' ? humanize(v) : String(v))).join(', ')
    : typeof value === 'boolean'
      ? value
        ? t`Yes`
        : t`No`
      : typeof value === 'string'
        ? humanize(value)
        : String(value ?? '-')
  return <span className="break-words">{text}</span>
}
