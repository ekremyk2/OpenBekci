import { useLingui } from '@lingui/react/macro'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'

/** Site switcher for site-scoped screens; hidden when the organization has a single site. */
export function SitePicker({
  sites,
  value,
  onChange,
}: {
  sites: { id: string; name: string }[]
  value: string | undefined
  onChange: (siteId: string) => void
}) {
  const { t } = useLingui()
  if (sites.length < 2) return null
  return (
    <div className="flex items-center gap-2">
      <Label htmlFor="site-picker">{t`Site`}</Label>
      <Select
        id="site-picker"
        className="w-auto"
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value)}
      >
        {sites.map((s) => (
          <option key={s.id} value={s.id}>
            {s.name}
          </option>
        ))}
      </Select>
    </div>
  )
}
