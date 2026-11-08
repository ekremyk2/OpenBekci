import { useId } from 'react'
import { useLingui } from '@lingui/react/macro'
import { cn } from '@/lib/utils'

export type DebtorSide = 'owner' | 'tenant'

/**
 * Owner or tenant side of a unit (M1). A unit has two running balances; every money screen that
 * acts on one picks it here.
 */
export function SidePicker({
  value,
  onChange,
  disabled,
  label,
  className,
}: {
  value: DebtorSide
  onChange: (side: DebtorSide) => void
  disabled?: boolean
  label?: string
  className?: string
}) {
  const { t } = useLingui()
  const name = useId()
  const options: { value: DebtorSide; label: string }[] = [
    { value: 'owner', label: t`Owner` },
    { value: 'tenant', label: t`Tenant` },
  ]
  return (
    <div
      role="radiogroup"
      aria-label={label ?? t`Side`}
      className={cn('inline-flex rounded-md border p-0.5', className)}
    >
      {options.map((o) => (
        <label
          key={o.value}
          className={cn(
            'cursor-pointer rounded px-3 py-1 text-sm has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring',
            value === o.value ? 'bg-primary text-primary-foreground' : 'hover:bg-accent',
            disabled && 'cursor-not-allowed opacity-50',
          )}
        >
          <input
            type="radio"
            name={name}
            value={o.value}
            checked={value === o.value}
            disabled={disabled}
            onChange={() => onChange(o.value)}
            className="sr-only"
          />
          {o.label}
        </label>
      ))}
    </div>
  )
}
