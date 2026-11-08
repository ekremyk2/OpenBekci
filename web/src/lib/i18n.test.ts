import { describe, expect, it } from 'vitest'
import { directionOf, intlLocale, matchLocale, resolveLocale } from './i18n'

describe('resolveLocale', () => {
  it('prefers the user setting', () => {
    expect(resolveLocale({ user: 'tr', browser: ['en-US'], orgDefault: 'en' })).toBe('tr')
  })
  it('then the browser, collapsing tr-TR to tr', () => {
    expect(resolveLocale({ browser: ['de-DE', 'tr-TR', 'en'], orgDefault: 'en' })).toBe('tr')
  })
  it('then the organization default', () => {
    expect(resolveLocale({ browser: ['de-DE', 'fr'], orgDefault: 'tr' })).toBe('tr')
  })
  it('then en', () => {
    expect(resolveLocale({ browser: ['de'], orgDefault: 'xx' })).toBe('en')
    expect(resolveLocale({})).toBe('en')
  })
  it('ignores unsupported user values', () => {
    expect(resolveLocale({ user: 'klingon', browser: ['tr'] })).toBe('tr')
  })
})

describe('locale helpers', () => {
  it('matchLocale', () => {
    expect(matchLocale('TR_tr')).toBe('tr')
    expect(matchLocale('pseudo-rtl')).toBe('pseudo-rtl')
    expect(matchLocale('')).toBeUndefined()
  })
  it('direction and Intl tag', () => {
    expect(directionOf('pseudo-rtl')).toBe('rtl')
    expect(directionOf('tr')).toBe('ltr')
    expect(intlLocale('pseudo')).toBe('en')
    expect(intlLocale('tr')).toBe('tr')
  })
})
