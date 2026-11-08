import { useCallback, useSyncExternalStore } from 'react'
import { useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import type { Site } from '@/api/schema'
import { Select } from '@/components/ui/select'

/*
 * "Current site" of the signed-in organization, remembered per organization in localStorage
 * (web plan §7). The operations screens (tickets, announcements, documents, notifications) are
 * site-scoped: every list call carries `site_id`.
 */
const KEY = 'ob.lastSite'
const listeners = new Set<() => void>()

function read(orgId: string | undefined): string | null {
  if (!orgId) return null
  try {
    return JSON.parse(localStorage.getItem(KEY) ?? '{}')[orgId] ?? null
  } catch {
    return null
  }
}

export function rememberSite(orgId: string, siteId: string) {
  try {
    const all = JSON.parse(localStorage.getItem(KEY) ?? '{}')
    all[orgId] = siteId
    localStorage.setItem(KEY, JSON.stringify(all))
  } catch {
    /* storage unavailable: the choice lasts for this page view only */
  }
  for (const l of listeners) l()
}

export interface CurrentSite {
  site: Site | undefined
  sites: Site[]
  setSiteId: (id: string) => void
  /** A resident's units at the current site (from /v1/me occupancies). */
  orgId: string | undefined
}

export function useSite(): CurrentSite {
  const { org } = useSession()
  const remembered = useSyncExternalStore(
    (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    () => read(org?.id),
    () => null,
  )
  const sites = org?.sites ?? []
  const site = sites.find((s) => s.id === remembered) ?? sites[0]
  const orgId = org?.id
  const setSiteId = useCallback(
    (id: string) => {
      if (orgId) rememberSite(orgId, id)
    },
    [orgId],
  )
  return { site, sites, setSiteId, orgId }
}

/** Site switcher; renders nothing for single-site organizations. */
export function SitePicker({ className }: { className?: string }) {
  const { t } = useLingui()
  const { site, sites, setSiteId } = useSite()
  if (sites.length < 2) return null
  return (
    <Select
      className={className ?? 'w-48'}
      aria-label={t`Site`}
      value={site?.id}
      onChange={(e) => setSiteId(e.target.value)}
    >
      {sites.map((s) => (
        <option key={s.id} value={s.id}>
          {s.name}
        </option>
      ))}
    </Select>
  )
}
