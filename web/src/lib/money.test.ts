import { describe, expect, it } from 'vitest'
import {
  currencyDigits,
  formatMoney,
  formatMoneyPlain,
  formatPercent,
  minorToDecimalString,
  parseMoneyInput,
} from './money'

// Intl output uses NBSP/narrow NBSP between number and symbol; normalise for readable asserts.
const norm = (s: string) => s.replace(/[\u00a0\u202f]/g, ' ')

describe('formatPercent', () => {
  it('follows the locale', () => {
    expect(formatPercent(2.5, 'en-US')).toBe('2.5%')
    expect(formatPercent(2.5, 'tr-TR')).toBe('%2,5')
  })
})

describe('formatMoney', () => {
  it('formats TRY in tr-TR', () => {
    expect(formatMoney(123456, 'TRY', 'tr-TR')).toBe('1.234,56 ₺')
  })
  it('formats TRY and USD in en-US', () => {
    expect(formatMoney(123456, 'TRY', 'en-US')).toBe(
      formatMoney(123456, 'TRY', 'tr-TR').replace('1.234,56', '1,234.56'),
    )
    expect(formatMoney(123456, 'USD', 'en-US')).toBe('$1,234.56')
  })
  it('keeps the sign and handles zero and sub-unit amounts', () => {
    expect(formatMoney(-5, 'USD', 'en-US')).toBe('-$0.05')
    expect(formatMoney(0, 'USD', 'en-US')).toBe('$0.00')
  })
  it('respects the currency exponent (JPY 0, KWD 3)', () => {
    expect(currencyDigits('JPY')).toBe(0)
    expect(currencyDigits('KWD')).toBe(3)
    expect(norm(formatMoney(1500, 'JPY', 'en-US'))).toBe('¥1,500')
  })
  it('is exact beyond 2^53 (int64 amounts)', () => {
    expect(formatMoney('9007199254740993', 'USD', 'en-US')).toBe('$90,071,992,547,409.93')
  })
  it('rejects unsafe JS numbers instead of rounding', () => {
    expect(() => formatMoney(2 ** 60, 'USD', 'en-US')).toThrow(RangeError)
  })
})

describe('minorToDecimalString', () => {
  it('pads and signs', () => {
    expect(minorToDecimalString(5, 2)).toBe('0.05')
    expect(minorToDecimalString(-123456n, 2)).toBe('-1234.56')
    expect(minorToDecimalString(7, 0)).toBe('7')
  })
})

describe('parseMoneyInput', () => {
  it('parses tr-TR separators', () => {
    expect(parseMoneyInput('1.234,56', 'TRY', 'tr-TR')).toBe(123456n)
    expect(parseMoneyInput('1.234', 'TRY', 'tr-TR')).toBe(123400n)
    expect(parseMoneyInput('₺ 12,5', 'TRY', 'tr-TR')).toBe(1250n)
  })
  it('accepts a lone dot as decimal in tr-TR when it cannot be grouping', () => {
    expect(parseMoneyInput('1.5', 'TRY', 'tr-TR')).toBe(150n)
  })
  it('parses en-US separators', () => {
    expect(parseMoneyInput('1,234.56', 'USD', 'en-US')).toBe(123456n)
    expect(parseMoneyInput('1234', 'USD', 'en-US')).toBe(123400n)
    expect(parseMoneyInput('-0.5', 'USD', 'en-US')).toBe(-50n)
  })
  it('rejects garbage and too many decimals', () => {
    expect(parseMoneyInput('', 'USD', 'en-US')).toBeNull()
    expect(parseMoneyInput('abc', 'USD', 'en-US')).toBeNull()
    expect(parseMoneyInput('1.234', 'USD', 'en-US')).toBeNull()
    expect(parseMoneyInput('12.5', 'JPY', 'en-US')).toBeNull()
  })
  it('round-trips with formatMoneyPlain', () => {
    const text = formatMoneyPlain(123456n, 'TRY', 'tr-TR')
    expect(text).toBe('1234,56')
    expect(parseMoneyInput(text, 'TRY', 'tr-TR')).toBe(123456n)
  })
})
