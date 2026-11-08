import { useCallback } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useSession } from '@/app/session'

const KEY = 'ob.lastSite'

function remembered(): string | null {
  try {
    return localStorage.getItem(KEY)
  } catch {
    return null
  }
}

/**
 * The site a site-scoped screen works on: the `site` search param, else the last one used, else the
 * organization's first site. The timezone and currency come from `/v1/me` (no extra request).
 */
export function useActiveSite(searchSite?: string) {
  const { org } = useSession()
  const navigate = useNavigate()
  const sites = org?.sites ?? []
  const site =
    sites.find((s) => s.id === searchSite) ?? sites.find((s) => s.id === remembered()) ?? sites[0]
  const setSite = useCallback(
    (siteId: string) => {
      try {
        localStorage.setItem(KEY, siteId)
      } catch {
        /* ignore */
      }
      // `site` is a search param of every screen that uses this hook; reset the page-specific rest.
      void navigate({
        to: '.',
        search: ((prev: Record<string, unknown>) => ({
          ...prev,
          site: siteId,
          cursor: undefined,
        })) as never,
      })
    },
    [navigate],
  )
  return {
    site,
    sites,
    siteId: site?.id,
    timezone: site?.timezone ?? 'UTC',
    currency: site?.currency ?? 'TRY',
    setSite,
  }
}
