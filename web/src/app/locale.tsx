import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from 'react'
import { useQuery } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { setLocaleProvider } from '@/api/client'
import { intlLocale, isLocale, loadLocale, resolveLocale, type Locale } from '@/lib/i18n'
import { meQueryOptions, useLastOrgId } from './session'

const KEY = 'ob.locale'

function stored(): string | null {
  try {
    return localStorage.getItem(KEY)
  } catch {
    return null
  }
}

/** Saved choice or browser languages, plus a `?locale=pseudo` developer override. */
export function initialLocale(): Locale {
  const fromQuery = new URLSearchParams(globalThis.location?.search).get('locale')
  if (isLocale(fromQuery)) return fromQuery
  return resolveLocale({ user: stored(), browser: navigator.languages })
}

interface LocaleContextValue {
  locale: Locale
  /** BCP 47 tag for Intl.* (pseudo locales format as English). */
  intl: string
  setLocale: (locale: Locale) => void
}
const LocaleContext = createContext<LocaleContextValue | null>(null)

const subscribe = (cb: () => void) => i18n.on('change', cb)

/**
 * The active locale lives in Lingui's global `i18n`; this provider re-resolves it when /v1/me
 * arrives (user → browser → org default → en) and exposes it plus a setter to the tree.
 * `initial` is only the fallback before a catalog has been activated.
 */
export function LocaleProvider({
  initial,
  children,
}: {
  initial: Locale
  children: React.ReactNode
}) {
  const active = useSyncExternalStore(subscribe, () => i18n.locale)
  const locale: Locale = isLocale(active) ? active : initial
  const [explicit, setExplicit] = useState<string | null>(stored)
  const me = useQuery(meQueryOptions).data

  const userSetting = explicit ?? me?.user.locale
  const selectedOrgId = useLastOrgId()
  const orgs = me?.organizations
  const orgDefault = (orgs?.find((o) => o.id === selectedOrgId) ?? orgs?.[0])?.default_locale
  useEffect(() => {
    if (new URLSearchParams(location.search).has('locale')) return
    const next = resolveLocale({ user: userSetting, browser: navigator.languages, orgDefault })
    if (next !== i18n.locale) void loadLocale(next)
  }, [userSetting, orgDefault])

  useEffect(() => {
    setLocaleProvider(() => intlLocale(i18n.locale))
  }, [])

  const setLocale = useCallback((next: Locale) => {
    try {
      localStorage.setItem(KEY, next)
    } catch {
      /* ignore */
    }
    setExplicit(next)
    void loadLocale(next)
  }, [])

  const value = useMemo(
    () => ({ locale, intl: intlLocale(locale), setLocale }),
    [locale, setLocale],
  )
  return (
    <LocaleContext.Provider value={value}>
      <I18nProvider i18n={i18n}>{children}</I18nProvider>
    </LocaleContext.Provider>
  )
}

export function useLocale(): LocaleContextValue {
  const ctx = useContext(LocaleContext)
  if (!ctx) throw new Error('useLocale outside LocaleProvider')
  return ctx
}
