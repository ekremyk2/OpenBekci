import { i18n } from '@lingui/core'

/** `pseudo-rtl` reuses the `pseudo` catalog and only flips the document direction. */
export const LOCALES = ['en', 'tr', 'pseudo', 'pseudo-rtl'] as const
export type Locale = (typeof LOCALES)[number]
/** Locales offered to people in the UI; pseudo locales are for developers (?locale= / storage). */
export const USER_LOCALES: readonly Locale[] = ['en', 'tr']

export const DEFAULT_LOCALE: Locale = 'en'
const RTL_LOCALES = new Set<string>(['pseudo-rtl', 'ar', 'he', 'fa', 'ur'])

export function isLocale(value: string | null | undefined): value is Locale {
  return !!value && (LOCALES as readonly string[]).includes(value)
}

/** "tr-TR" → "tr"; unsupported or empty → undefined. Fallback chain tr-TR → tr → en. */
export function matchLocale(tag: string | null | undefined): Locale | undefined {
  if (!tag) return undefined
  const lower = tag.toLowerCase()
  if (isLocale(lower)) return lower
  const base = lower.split(/[-_]/)[0]
  return isLocale(base) ? base : undefined
}

export interface LocaleInputs {
  /** Explicit user setting (profile, or a locally saved choice when signed out). */
  user?: string | null
  /** navigator.languages, in preference order. */
  browser?: readonly string[]
  /** Organization default from /v1/me. */
  orgDefault?: string | null
}

/** user setting → browser → organization default → en (overview §6.6). */
export function resolveLocale({ user, browser = [], orgDefault }: LocaleInputs): Locale {
  const fromUser = matchLocale(user)
  if (fromUser) return fromUser
  for (const tag of browser) {
    const m = matchLocale(tag)
    if (m) return m
  }
  return matchLocale(orgDefault) ?? DEFAULT_LOCALE
}

export function directionOf(locale: string): 'ltr' | 'rtl' {
  return RTL_LOCALES.has(locale) ? 'rtl' : 'ltr'
}

/** BCP 47 tag handed to Intl: pseudo locales format like English. */
export function intlLocale(locale: string): string {
  return locale.startsWith('pseudo') ? 'en' : locale
}

type CatalogModule = { messages: Record<string, string> }
const catalogs = import.meta.glob<CatalogModule>('../locales/*/messages.po')

export async function loadLocale(locale: Locale): Promise<void> {
  const catalogName = locale === 'pseudo-rtl' ? 'pseudo' : locale
  const loader = catalogs[`../locales/${catalogName}/messages.po`]
  if (loader) {
    const { messages } = await loader()
    i18n.load(locale, messages)
  } else {
    i18n.load(locale, {})
  }
  i18n.activate(locale)
  applyDocumentLocale(locale)
}

export function applyDocumentLocale(locale: string) {
  if (typeof document === 'undefined') return
  document.documentElement.lang = intlLocale(locale)
  document.documentElement.dir = directionOf(locale)
}
