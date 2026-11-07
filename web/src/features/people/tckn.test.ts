import { describe, expect, it } from 'vitest'
import { isValidNationalId, isValidTckn } from './tckn'
import { formatPlate } from '@/features/units/labels'
import { maskIban } from '@/features/sites/SiteTabs'
import { parsePasted, parseWeight } from '@/features/setup/keys'

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
    expect(parseWeight('1.234,5')).toBe(1234.5)
    expect(parseWeight('12,5')).toBe(12.5)
    expect(parseWeight('abc')).toBeNaN()
  })
})
