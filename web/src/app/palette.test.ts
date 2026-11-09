import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { ACCENTS } from './theme'

// Reads the real tokens out of src/index.css and checks WCAG AA (4.5:1) for every text/background
// pair, in light and dark, for the default and each accent palette.
const css = readFileSync('src/index.css', 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
const blocks = new Map<string, Record<string, string>>()
for (const [, sel, body] of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
  const vars: Record<string, string> = {}
  for (const [, k, v] of body!.matchAll(/(--[\w-]+):\s*([^;]+);/g)) vars[k!] = v!.trim()
  // the first selector is preceded by the `@import ...;` lines
  blocks.set(sel!.split(';').pop()!.trim().replace(/\s+/g, ' '), vars)
}

function tokens(dark: boolean, accent: string): Record<string, string> {
  const layers = [':root', dark ? '.dark' : '']
  if (accent !== 'brand') {
    layers.push(`[data-accent='${accent}']`)
    if (dark) layers.push(`.dark[data-accent='${accent}']`)
  }
  if (dark && accent !== 'brand') layers.push('.dark[data-accent]')
  return Object.assign({}, ...layers.map((l) => blocks.get(l) ?? {}))
}

function channel(c: number) {
  return c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055
}

/** Relative luminance of `#rrggbb` or `oklch(L C H)`, after resolving `var(--x)`. */
function luminance(value: string, vars: Record<string, string>): number {
  const v = value.replace(/var\((--[\w-]+)\)/g, (_, n: string) => vars[n]!)
  let rgb: [number, number, number]
  const hex = /^#([0-9a-f]{6})$/i.exec(v)
  if (hex) {
    const n = parseInt(hex[1]!, 16)
    rgb = [(n >> 16) / 255, ((n >> 8) & 255) / 255, (n & 255) / 255]
  } else {
    const m = /^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)$/.exec(v)
    if (!m) throw new Error(`cannot parse ${value}`)
    const [L, C, H] = [Number(m[1]), Number(m[2]), (Number(m[3]) * Math.PI) / 180]
    const a = C * Math.cos(H)
    const b = C * Math.sin(H)
    const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
    const mm = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
    const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
    const lin = [
      4.0767416621 * l - 3.3077115913 * mm + 0.2309699292 * s,
      -1.2684380046 * l + 2.6097574011 * mm - 0.3413193965 * s,
      -0.0041960863 * l - 0.7034186147 * mm + 1.707614701 * s,
    ]
    rgb = lin.map((x) => Math.min(1, Math.max(0, channel(Math.max(0, x))))) as typeof rgb
  }
  const [r, g, bl] = rgb.map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4))
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * bl!
}

function ratio(fg: string, bg: string, vars: Record<string, string>) {
  const [a, b] = [luminance(fg, vars), luminance(bg, vars)]
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}

const PAIRS: [string, string][] = [
  ['--foreground', '--background'],
  ['--card-foreground', '--card'],
  ['--popover-foreground', '--popover'],
  ['--muted-foreground', '--background'],
  ['--muted-foreground', '--card'],
  ['--muted-foreground', '--muted'],
  ['--secondary-foreground', '--secondary'],
  ['--accent-foreground', '--accent'],
  ['--sidebar-foreground', '--sidebar'],
  ['--primary-foreground', '--primary'],
  ['--destructive-foreground', '--destructive'],
  ['--destructive', '--background'],
  ['--success', '--background'],
  ['--warning', '--background'],
  ['--info', '--background'],
]

describe('palette contrast (WCAG AA 4.5:1)', () => {
  for (const dark of [false, true]) {
    for (const accent of ACCENTS) {
      it(`${dark ? 'dark' : 'light'} / ${accent}`, () => {
        const vars = tokens(dark, accent)
        // primary as text (links, outline buttons); the organization's own brand color is not ours to judge
        const pairs = accent === 'brand' ? PAIRS : [...PAIRS, ['--primary', '--background']]
        for (const [fg, bg] of pairs as [string, string][]) {
          expect(ratio(vars[fg]!, vars[bg]!, vars), `${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5)
        }
      })
    }
  }

  it('has no violet accent', () => {
    expect(ACCENTS).not.toContain('violet')
    for (const [sel, vars] of blocks) {
      for (const v of Object.values(vars)) {
        const hue = /^oklch\(\s*[\d.]+\s+[\d.]+\s+([\d.]+)/.exec(v)?.[1]
        if (hue && sel.includes('accent')) expect(Number(hue)).not.toBeGreaterThan(280)
      }
    }
  })
})
