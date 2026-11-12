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
    legal_preset: t`Legal preset`,
    schedule: t`Meeting schedule`,
    transfer_rule: t`Transfer rule`,
    vote_weight: t`Vote weight`,
    default_monthly_rate_bps: t`Default monthly rate (bps)`,
    monthly_rate_ceiling_bps: t`Monthly rate ceiling (bps)`,
    pro_rata: t`Pro rata`,
    basis: t`Basis`,
    numerator: t`Numerator`,
    denominator: t`Denominator`,
    of: t`Of`,
    by_topic: t`By topic`,
    default: t`Default`,
    budget: t`Budget`,
    election: t`Election`,
    routine: t`Routine`,
    plan_amendment: t`Plan amendment`,
    common_area_change: t`Common area change`,
    main_structure_change: t`Main structure change`,
    min_notice_days: t`Minimum notice (days)`,
    ordinary_assembly_month: t`Ordinary assembly month`,
    second_meeting_min_days: t`Second meeting, earliest (days)`,
    second_meeting_max_days: t`Second meeting, latest (days)`,
    co_owner_representation: t`Co-owner representation`,
    per_person_cap_numerator: t`Per-person cap, numerator`,
    per_person_cap_denominator: t`Per-person cap, denominator`,
    per_person_cap_percent: t`Per-person cap (%)`,
    proxy_cap_per_person: t`Proxies per person`,
    proxy_cap_percent: t`Proxy cap (%)`,
    proxy_small_building_unit_limit: t`Small building (units)`,
    weight: t`Weight`,
    debt_on_move_out: t`Debt on move-out`,
  }
  return (k: string) => known[k] ?? humanize(k)
}

/** Readable text for the enum-like string values presets use; unknown values are humanized. */
function useValueLabel() {
  const { t } = useLingui()
  const known: Record<string, string> = {
    units_and_share: t`Units and share`,
    none: t`None`,
    all: t`All`,
    present: t`Present`,
    per_unit: t`Per unit`,
    single_representative: t`Single representative`,
    stays_with_person: t`Stays with the person`,
    tr: t`Türkiye`,
  }
  return (v: string) => known[v] ?? humanize(v)
}

/** A pack default's value as labelled fields (nested objects nest); wraps, never overflows. */
export function PackValue({ value, raw }: { value: unknown; raw?: boolean }) {
  const label = useSettingLabel()
  const { t } = useLingui()
  const valueLabel = useValueLabel()
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
    ? value.map((v) => (typeof v === 'string' ? valueLabel(v) : String(v))).join(', ')
    : typeof value === 'boolean'
      ? value
        ? t`Yes`
        : t`No`
      : typeof value === 'string'
        ? valueLabel(value)
        : String(value ?? '-')
  return <span className="break-words">{text}</span>
}
