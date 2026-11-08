import { Trans, useLingui } from '@lingui/react/macro'
import type { DocumentVisibility } from '@/api/draft'
import { Checkbox } from '@/components/ui/checkbox'
import { Select } from '@/components/ui/select'
import { Label } from '@/components/ui/label'
import { useUnits } from '@/features/dashboard/reference'

/** Who may see a folder or document: board only, owners, everyone, or chosen units (O8). */
export function VisibilityEditor({
  orgId,
  siteId,
  value,
  onChange,
}: {
  orgId: string
  siteId: string
  value: DocumentVisibility
  onChange: (next: DocumentVisibility) => void
}) {
  const { t } = useLingui()
  const units = useUnits(orgId, siteId, value.level === 'units')
  return (
    <div className="flex flex-col gap-2">
      <Select
        aria-label={t`Visible to`}
        value={value.level}
        onChange={(e) =>
          onChange({
            level: e.target.value as DocumentVisibility['level'],
            unit_ids: e.target.value === 'units' ? (value.unit_ids ?? []) : undefined,
          })
        }
      >
        <option value="board">{t`Board only`}</option>
        <option value="owners">{t`Owners`}</option>
        <option value="everyone">{t`Everyone`}</option>
        <option value="units">{t`Chosen units`}</option>
      </Select>
      {value.level === 'units' ? (
        <fieldset className="flex max-h-40 flex-col gap-1 overflow-y-auto rounded-md border p-2">
          <legend className="px-1 text-sm">
            <Trans>Units</Trans>
          </legend>
          {units.data?.map((u) => (
            <Label key={u.id} className="gap-2 font-normal">
              <Checkbox
                checked={value.unit_ids?.includes(u.id) ?? false}
                onChange={(e) => {
                  const set = new Set(value.unit_ids ?? [])
                  if (e.target.checked) set.add(u.id)
                  else set.delete(u.id)
                  onChange({ level: 'units', unit_ids: [...set] })
                }}
              />
              {u.door_number}
            </Label>
          ))}
          {units.data && units.data.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              <Trans>No units yet.</Trans>
            </p>
          ) : null}
        </fieldset>
      ) : null}
    </div>
  )
}
