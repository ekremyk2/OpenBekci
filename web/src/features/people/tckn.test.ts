import { describe, expect, it } from 'vitest'
import { isValidNationalId, isValidTckn } from './tckn'
import { formatPlate } from '@/features/units/labels'
import { maskIban } from '@/features/sites/SiteTabs'
import { formatWeight, parsePasted, parseWeight } from '@/features/setup/keys'

describe('TCKN checksum', () => {
  it('accepts a valid number and rejects wrong checksums, short and zero-leading numbers', () => {
    expect(isValidTckn('10000000146')).toBe(true)
    expect(isValidTckn('10000000147')).toBe(false)
    expect(isValidTckn('1234567890')).toBe(false)
    expect(isValidTckn('01000000146')).toBe(false)
    expect(isValidNationalId('TR', '100 000 001 46')).toBe(true)
    expect(isValidNationalId('DE', 'T22000129')).toBe(true)
  })
})

describe('small helpers', () => {
  it('formats plates and masks IBANs', () => {
    expect(formatPlate('34ABC123')).toBe('34 ABC 123')
    expect(maskIban('TR33 0006 1005 1978 6457 8413 26')).toBe('TR33 •••• •••• •••• 1326')
  })
  it('parses pasted spreadsheet cells and decimal commas', () => {
    expect(parsePasted('A-1\t100\r\nA-2\t120,5\n')).toEqual([
      { door: 'A-1', value: '100' },
      { door: 'A-2', value: '120,5' },
    ])
    expect(parsePasted('1\n2')).toEqual([
      { door: null, value: '1' },
      { door: null, value: '2' },
    ])
    expect(parseWeight('abc')).toBeNaN()
  })
  it('parses weights by the active locale and rejects ambiguous input', () => {
    // both separators present: the last one is the decimal, in either convention
    expect(parseWeight('1.234,5', 'tr')).toBe(1234.5)
    expect(parseWeight('1,234.5', 'tr')).toBe(1234.5)
    expect(parseWeight('1.234,5', 'en')).toBe(1234.5)
    expect(parseWeight('1,234.5', 'en')).toBe(1234.5)
    // a lone separator takes the locale's meaning
    expect(parseWeight('1.234', 'tr')).toBe(1234)
    expect(parseWeight('1,234', 'tr')).toBe(1.234)
    expect(parseWeight('1,234', 'en')).toBe(1234)
    expect(parseWeight('1.234', 'en')).toBe(1.234)
    expect(parseWeight('12,5', 'tr')).toBe(12.5)
    expect(parseWeight('1.234.567', 'tr')).toBe(1234567)
    expect(parseWeight('100', 'tr')).toBe(100)
    // ambiguous or malformed input is rejected, never guessed
    expect(parseWeight('12,5', 'en')).toBeNaN()
    expect(parseWeight('1234.5', 'tr')).toBeNaN()
    expect(parseWeight('1.2.3', 'en')).toBeNaN()
    expect(parseWeight('1,234,5', 'en')).toBeNaN()
    expect(parseWeight('1/3')).toBeNaN()
    expect(parseWeight('')).toBeNaN()
    expect(formatWeight(1234.5, 'tr')).toBe('1234,5')
    expect(formatWeight(1234.5, 'en')).toBe('1234.5')
  })
})
