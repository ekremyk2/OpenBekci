import { useSyncExternalStore } from 'react'

export type ThemePreference = 'system' | 'light' | 'dark'
export const ACCENTS = ['brand', 'slate', 'forest', 'terracotta', 'navy'] as const
export type Accent = (typeof ACCENTS)[number]
const KEY = 'ob.theme' // keep in sync with public/theme-init.js
const ACCENT_KEY = 'ob.accent'

const listeners = new Set<() => void>()
const media =
  typeof window !== 'undefined' ? window.matchMedia('(prefers-color-scheme: dark)') : null

function readPreference(): ThemePreference {
  try {
    const v = localStorage.getItem(KEY)
    return v === 'light' || v === 'dark' ? v : 'system'
  } catch {
    return 'system'
  }
}

function readAccent(): Accent {
  try {
    const v = localStorage.getItem(ACCENT_KEY) as Accent | null
    return v && ACCENTS.includes(v) ? v : 'brand'
  } catch {
    return 'brand'
  }
}

let preference: ThemePreference = readPreference()
let accent: Accent = readAccent()

function resolve(p: ThemePreference): 'light' | 'dark' {
  return p === 'system' ? (media?.matches ? 'dark' : 'light') : p
}

function apply() {
  document.documentElement.classList.toggle('dark', resolve(preference) === 'dark')
  if (accent === 'brand') document.documentElement.removeAttribute('data-accent')
  else document.documentElement.setAttribute('data-accent', accent)
}

export function setAccent(next: Accent) {
  accent = next
  try {
    if (next === 'brand') localStorage.removeItem(ACCENT_KEY)
    else localStorage.setItem(ACCENT_KEY, next)
  } catch {
    /* storage blocked: lasts for this page load */
  }
  apply()
  listeners.forEach((l) => l())
}

export function setThemePreference(next: ThemePreference) {
  preference = next
  try {
    if (next === 'system') localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, next)
  } catch {
    /* storage blocked: preference lasts for this page load */
  }
  apply()
  listeners.forEach((l) => l())
}

media?.addEventListener('change', () => {
  apply()
  listeners.forEach((l) => l())
})

let snapshotKey = ''
let snapshot = { preference, resolved: resolve(preference), accent }
function getSnapshot() {
  const key = `${preference}:${resolve(preference)}:${accent}`
  if (key !== snapshotKey) {
    snapshotKey = key
    snapshot = { preference, resolved: resolve(preference), accent }
  }
  return snapshot
}

export function useTheme() {
  const s = useSyncExternalStore(
    (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    getSnapshot,
    getSnapshot,
  )
  return { ...s, setPreference: setThemePreference, setAccent }
}

/** Relative luminance of #rgb / #rrggbb, or null when the string is not a hex color. */
function luminance(hex: string): number | null {
  const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(hex.trim())
  if (!m) return null
  const h = m[1]!.length === 3 ? [...m[1]!].map((c) => c + c).join('') : m[1]!
  const [r, g, b] = [0, 2, 4].map((i) => {
    const c = parseInt(h.slice(i, i + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }) as [number, number, number]
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

const DARK_FOREGROUND = '#111827'
const LIGHT_FOREGROUND = '#ffffff'

function contrast(l1: number, l2: number): number {
  const [hi, lo] = l1 > l2 ? [l1, l2] : [l2, l1]
  return (hi + 0.05) / (lo + 0.05)
}

/** Foreground (white or near-black) with the higher WCAG contrast ratio against the brand color. */
export function brandForeground(hex: string): string {
  const l = luminance(hex)
  if (l === null) return LIGHT_FOREGROUND
  const dark = luminance(DARK_FOREGROUND)!
  const light = luminance(LIGHT_FOREGROUND)!
  return contrast(l, dark) > contrast(l, light) ? DARK_FOREGROUND : LIGHT_FOREGROUND
}

/**
 * Applies `branding.primary_color` from the discovery document. An absent or invalid value resets
 * to the stylesheet defaults.
 */
export function applyBranding(primaryColor: string | undefined) {
  const root = document.documentElement.style
  if (!primaryColor || luminance(primaryColor) === null) {
    root.removeProperty('--brand-seed')
    root.removeProperty('--brand-seed-foreground')
    return
  }
  root.setProperty('--brand-seed', primaryColor)
  root.setProperty('--brand-seed-foreground', brandForeground(primaryColor))
}
