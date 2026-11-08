import { useId, useMemo, useState } from 'react'
import { useLingui } from '@lingui/react/macro'
import { cn } from '@/lib/utils'

export interface PickerItem {
  id: string
  label: string
}

/**
 * A searchable combobox for picking one entity (unit, person, account, vendor...). A site can have
 * far more than 200 units, so a plain list is not enough:
 *  - `onQuery` hands the typed text to the server (contract `q`); without it the list is filtered here.
 *  - `truncated` says the server still has more than it sent, and the list says so
 *    ("showing the first N, type to narrow it") instead of silently cutting it off.
 */
export function EntityPicker({
  id,
  value,
  onChange,
  items,
  onQuery,
  loading = false,
  truncated = false,
  placeholder,
  disabled,
  className,
  ...aria
}: {
  id?: string
  value: string
  onChange: (id: string) => void
  items: PickerItem[]
  onQuery?: (q: string) => void
  loading?: boolean
  truncated?: boolean
  placeholder?: string
  disabled?: boolean
  className?: string
  'aria-label'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
}) {
  const { t } = useLingui()
  const listId = useId()
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [active, setActive] = useState(0)
  // the label of the last choice stays known while a server search swaps the item list
  const [chosen, setChosen] = useState<PickerItem | null>(null)
  const selectedLabel = !value
    ? ''
    : (items.find((i) => i.id === value)?.label ?? (chosen?.id === value ? chosen.label : ''))

  const shown = useMemo(() => {
    if (onQuery || text === '') return items
    const needle = text.toLocaleLowerCase()
    return items.filter((i) => i.label.toLocaleLowerCase().includes(needle))
  }, [items, onQuery, text])

  function choose(next: string) {
    const item = items.find((i) => i.id === next)
    if (item) setChosen(item)
    onChange(next)
    setOpen(false)
    setText('')
    onQuery?.('')
  }

  return (
    <div className={cn('relative', className)}>
      <input
        {...aria}
        id={id}
        role="combobox"
        aria-expanded={open}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={open && shown[active] ? `${listId}-${shown[active].id}` : undefined}
        autoComplete="off"
        disabled={disabled}
        placeholder={placeholder ?? t`Select…`}
        value={open ? text : selectedLabel}
        className="flex h-9 w-full min-w-40 rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-none focus-visible:ring-[3px] focus-visible:ring-ring disabled:opacity-50"
        onFocus={() => {
          setText('')
          setOpen(true)
        }}
        onBlur={() => setOpen(false)}
        onChange={(e) => {
          setText(e.target.value)
          setActive(0)
          setOpen(true)
          onQuery?.(e.target.value)
          if (e.target.value === '' && value) onChange('')
        }}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') {
            e.preventDefault()
            setOpen(true)
            setActive((a) => Math.min(a + 1, Math.max(shown.length - 1, 0)))
          } else if (e.key === 'ArrowUp') {
            e.preventDefault()
            setActive((a) => Math.max(a - 1, 0))
          } else if (e.key === 'Enter' && open && shown[active]) {
            e.preventDefault()
            choose(shown[active].id)
          } else if (e.key === 'Escape' && open) {
            e.stopPropagation()
            setOpen(false)
          }
        }}
      />
      {open ? (
        <ul
          id={listId}
          role="listbox"
          className="absolute inset-x-0 top-full z-50 mt-1 max-h-64 overflow-auto rounded-md border bg-popover p-1 text-sm text-popover-foreground shadow-md"
        >
          {loading ? (
            <li className="px-2 py-1.5 text-muted-foreground" role="presentation">
              {t`Loading…`}
            </li>
          ) : shown.length === 0 ? (
            <li className="px-2 py-1.5 text-muted-foreground" role="presentation">
              {t`Nothing found`}
            </li>
          ) : (
            shown.map((i, n) => (
              <li
                key={i.id}
                id={`${listId}-${i.id}`}
                role="option"
                aria-selected={i.id === value}
                className={cn(
                  'cursor-pointer rounded-sm px-2 py-1.5',
                  n === active && 'bg-accent text-accent-foreground',
                )}
                // mousedown, not click: the input must not lose focus before the choice lands
                onMouseDown={(e) => {
                  e.preventDefault()
                  choose(i.id)
                }}
                onMouseEnter={() => setActive(n)}
              >
                {i.label}
              </li>
            ))
          )}
          {truncated && !loading ? (
            <li className="border-t px-2 py-1.5 text-xs text-muted-foreground" role="presentation">
              {t`Showing the first ${items.length}: type to refine the search.`}
            </li>
          ) : null}
        </ul>
      ) : null}
    </div>
  )
}
