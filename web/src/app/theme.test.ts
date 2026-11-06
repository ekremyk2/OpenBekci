import { afterEach, describe, expect, it } from 'vitest'
import { applyBranding, brandForeground } from './theme'

afterEach(() => document.documentElement.removeAttribute('style'))

describe('brandForeground', () => {
  it('picks the foreground with the higher WCAG contrast ratio', () => {
    expect(brandForeground('#ffffff')).toBe('#111827')
    expect(brandForeground('#000000')).toBe('#ffffff')
    expect(brandForeground('#ffff00')).toBe('#111827')
    expect(brandForeground('#0f766e')).toBe('#ffffff')
  })

  it('does not use a luminance threshold', () => {
    expect(brandForeground('#2563eb')).toBe('#ffffff')
    expect(brandForeground('#f97316')).toBe('#111827')
    // luminance ~0.32: below a 0.4 threshold (white), but dark text has the better contrast
    expect(brandForeground('#9a9a9a')).toBe('#111827')
  })
})

describe('applyBranding', () => {
  it('sets the seed and its foreground', () => {
    applyBranding('#0f766e')
    const style = document.documentElement.style
    expect(style.getPropertyValue('--brand-seed')).toBe('#0f766e')
    expect(style.getPropertyValue('--brand-seed-foreground')).toBe('#ffffff')
  })

  it('resets when absent or not a hex colour', () => {
    applyBranding('#0f766e')
    applyBranding('red; background: url(x)')
    expect(document.documentElement.style.getPropertyValue('--brand-seed')).toBe('')
    applyBranding('#0f766e')
    applyBranding(undefined)
    expect(document.documentElement.style.getPropertyValue('--brand-seed-foreground')).toBe('')
  })
})
