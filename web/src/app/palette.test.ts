import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// Reads the real brand tokens out of src/index.css and checks the brand's contrast pairs
// (brand/README.md) in light and dark: 4.5:1 for text, 3:1 for controls, focus and placeholders.
const css = readFileSync('src/index.css', 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
const blocks = new Map<string, Record<string, string>>()
for (const [, sel, body] of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
  const vars: Record<string, string> = {}
  for (const [, k, v] of body!.matchAll(/(--[\w-]+):\s*([^;]+);/g)) vars[k!] = v!.trim()
  blocks.set(sel!.split(';').pop()!.trim().replace(/\s+/g, ' '), vars)
}

function tokens(dark: boolean): Record<string, string> {
  return Object.assign({}, blocks.get(':root'), dark ? blocks.get('.dark') : {})
}

function luminance(token: string, vars: Record<string, string>): number {
  const v = vars[`--${token}`]!
  const hex = /^#([0-9a-f]{6})$/i.exec(v)
  if (!hex) throw new Error(`--${token} is not a #rrggbb value: ${v}`)
  const n = parseInt(hex[1]!, 16)
  const [r, g, b] = [n >> 16, (n >> 8) & 255, n & 255].map((c) => {
    c /= 255
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!
}

function ratio(fg: string, bg: string, vars: Record<string, string>) {
  const [a, b] = [luminance(fg, vars), luminance(bg, vars)]
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}

const TEXT: [string, string][] = [
  ['ink', 'surface'],
  ['ink', 'surface-raised'],
  ['ink', 'surface-sunken'],
  ['ink', 'tile-tint'],
  ['ink-muted', 'surface'],
  ['ink-muted', 'surface-raised'],
  ['tile', 'surface'],
  ['tile', 'surface-raised'],
  ['tile-deep', 'tile-tint'],
  ['on-tile', 'tile'],
  ['on-tile', 'tile-deep'],
  ['on-night', 'night'],
  ['on-tile', 'danger'],
  ['success', 'surface'],
  ['success', 'success-tint'],
  ['warning', 'surface'],
  ['warning', 'warning-tint'],
  ['danger', 'surface'],
  ['danger', 'danger-tint'],
  ['info', 'surface'],
  ['info', 'info-tint'],
]
const NON_TEXT: [string, string][] = [
  ['focus', 'surface'],
  ['focus', 'surface-raised'],
  ['line-strong', 'surface'],
  ['line-strong', 'surface-raised'],
  ['ink-faint', 'surface'],
]

describe('brand palette contrast', () => {
  for (const dark of [false, true]) {
    it(`${dark ? 'dark' : 'light'}: text pairs reach 4.5:1`, () => {
      const vars = tokens(dark)
      for (const [fg, bg] of TEXT) {
        expect(ratio(fg, bg, vars), `${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5)
      }
    })
    it(`${dark ? 'dark' : 'light'}: controls, focus and placeholders reach 3:1`, () => {
      const vars = tokens(dark)
      for (const [fg, bg] of NON_TEXT) {
        expect(ratio(fg, bg, vars), `${fg} on ${bg}`).toBeGreaterThanOrEqual(3)
      }
    })
  }

  it('has one accent and no violet, gradient or pure black/white ground', () => {
    expect(css).not.toMatch(/data-accent|gradient/)
    for (const vars of blocks.values()) {
      for (const v of Object.values(vars)) {
        const hex = /^#([0-9a-f]{6})$/i.exec(v)?.[1]
        if (!hex) continue
        const [r, g, b] = [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16))
        expect(r! > 110 && b! > r! && b! > g! && r! > g! + 20, `${v} reads violet`).toBe(false)
      }
    }
    expect(tokens(false)['--surface']).not.toBe('#ffffff')
  })
})
