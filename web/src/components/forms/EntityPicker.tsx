import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { XIcon } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

export interface PickerOption {
  id: string
  label: string
  hint?: string
}

function useDebounced<T>(value: T, ms = 250): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const h = setTimeout(() => setV(value), ms)
    return () => clearTimeout(h)
  }, [value, ms])
  return v
}

/**
 * Search-as-you-type picker for server-side entities (units, persons). Shows the chosen entity as a
 * chip; typing lists matches from `search`. Keyboard: Tab to the results, Enter/Space to choose.
 */
export function EntityPicker({
  id,
  queryKey,
  value,
  onChange,
  search,
  placeholder,
  disabled,
  invalid,
  'aria-label': ariaLabel,
  pageSize,
}: {
  id: string
  /** Distinguishes the cache entries (include the site or org id). */
  queryKey: readonly unknown[]
  value: PickerOption | null
  onChange: (option: PickerOption | null) => void
  search: (q: string) => Promise<PickerOption[]>
  placeholder?: string
  disabled?: boolean
  invalid?: boolean
  'aria-label'?: string
  /** The page size `search` asks for: a full page means the server has more, and the list says so. */
  pageSize?: number
}) {
  const { t } = useLingui()
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const q = useDebounced(text)
  const results = useQuery({
    queryKey: [...queryKey, 'picker', q],
    queryFn: () => search(q),
    enabled: open && !value,
    staleTime: 10_000,
  })

  if (value) {
    return (
      <div className="flex h-9 items-center justify-between gap-2 rounded-md border px-3 text-sm">
        <output id={id} aria-label={ariaLabel} className="truncate">
          {value.label}
        </output>
        {disabled ? null : (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-6"
            aria-label={t`Clear selection`}
            onClick={() => {
              onChange(null)
              setText('')
            }}
          >
            <XIcon />
          </Button>
        )}
      </div>
    )
  }
  return (
    <div className="relative">
      <Input
        id={id}
        value={text}
        disabled={disabled}
        aria-invalid={invalid || undefined}
        aria-label={ariaLabel}
        placeholder={placeholder ?? t`Search…`}
        autoComplete="off"
        onFocus={() => setOpen(true)}
        onClick={() => setOpen(true)}
        onChange={(e) => {
          setText(e.target.value)
          setOpen(true)
        }}
      />
      {open ? (
        <ul
          aria-label={t`Search results`}
          className="absolute inset-x-0 top-full z-20 mt-1 max-h-60 overflow-y-auto rounded-md border bg-popover p-1 shadow-md"
        >
          {results.isLoading ? (
            <li className="p-2 text-sm text-muted-foreground">{t`Searching…`}</li>
          ) : (results.data ?? []).length === 0 ? (
            <li className="p-2 text-sm text-muted-foreground">{t`No matches`}</li>
          ) : (
            (results.data ?? []).map((o) => (
              <li key={o.id}>
                <button
                  type="button"
                  className="flex w-full items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-start text-sm hover:bg-accent focus-visible:bg-accent focus-visible:outline-none"
                  onClick={() => {
                    onChange(o)
                    setOpen(false)
                  }}
                >
                  <span>{o.label}</span>
                  {o.hint ? <span className="text-xs text-muted-foreground">{o.hint}</span> : null}
                </button>
              </li>
            ))
          )}
          {pageSize && (results.data ?? []).length >= pageSize ? (
            <li className="border-t px-2 py-1.5 text-xs text-muted-foreground">
              {t`Showing the first ${(results.data ?? []).length}: type to refine the search.`}
            </li>
          ) : null}
        </ul>
      ) : null}
    </div>
  )
}

/** Units of one site (label: door number). */
export function UnitPicker({
  orgId,
  siteId,
  ...rest
}: { orgId: string; siteId: string } & Omit<
  React.ComponentProps<typeof EntityPicker>,
  'search' | 'queryKey'
>) {
  return (
    <EntityPicker
      {...rest}
      pageSize={20}
      queryKey={['unit-picker', orgId, siteId]}
      search={async (q) => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/units', {
          params: {
            path: { org_id: orgId },
            query: { site_id: siteId, q: q || undefined, limit: 20 },
          },
        })
        return (data?.items ?? []).map((u) => ({ id: u.id, label: u.door_number, hint: u.type }))
      }}
    />
  )
}

/** Persons of the organization (label: display name). */
export function PersonPicker({
  orgId,
  siteId,
  ...rest
}: { orgId: string; siteId?: string } & Omit<
  React.ComponentProps<typeof EntityPicker>,
  'search' | 'queryKey'
>) {
  return (
    <EntityPicker
      {...rest}
      pageSize={20}
      queryKey={['person-picker', orgId, siteId ?? null]}
      search={async (q) => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/persons', {
          params: {
            path: { org_id: orgId },
            query: { site_id: siteId, q: q || undefined, limit: 20 },
          },
        })
        return (data?.items ?? []).map((p) => ({
          id: p.id,
          label: p.display_name,
          hint: p.contact.phone ?? p.contact.email ?? undefined,
        }))
      }}
    />
  )
}
