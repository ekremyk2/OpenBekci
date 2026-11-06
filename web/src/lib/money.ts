/**
 * Money is always integer minor units + an ISO 4217 currency (backend §9). Nothing here uses
 * floating point for amounts: minor units go to Intl as exact decimal strings.
 */

export type Minor = bigint | number | string

/** Currency exponent (digits after the decimal point) as Intl knows it: TRY 2, JPY 0, KWD 3. */
export function currencyDigits(currency: string): number {
  return (
    new Intl.NumberFormat('en', { style: 'currency', currency }).resolvedOptions()
      .maximumFractionDigits ?? 2
  )
}

export function toMinorBigInt(minor: Minor): bigint {
  if (typeof minor === 'bigint') return minor
  if (typeof minor === 'number') {
    if (!Number.isSafeInteger(minor)) throw new RangeError(`Not an integer amount: ${minor}`)
    return BigInt(minor)
  }
  return BigInt(minor)
}

/** `-123456`, 2 digits → `"-1234.56"` (exact decimal string accepted by Intl.NumberFormat). */
export function minorToDecimalString(minor: Minor, digits: number): string {
  const value = toMinorBigInt(minor)
  const negative = value < 0n
  const abs = (negative ? -value : value).toString().padStart(digits + 1, '0')
  const whole = abs.slice(0, abs.length - digits)
  const frac = digits > 0 ? abs.slice(abs.length - digits) : ''
  return `${negative ? '-' : ''}${whole}${frac ? `.${frac}` : ''}`
}

export function formatMoney(
  minor: Minor,
  currency: string,
  locale: string,
  options: Intl.NumberFormatOptions = {},
): string {
  const digits = currencyDigits(currency)
  const nf = new Intl.NumberFormat(locale, { style: 'currency', currency, ...options })
  // Intl accepts exact decimal strings; the cast bridges lib.d.ts, which only declares number|bigint.
  return nf.format(minorToDecimalString(minor, digits) as unknown as number)
}

function separators(locale: string) {
  const parts = new Intl.NumberFormat(locale, { useGrouping: true }).formatToParts(1234567.5)
  return {
    group: parts.find((p) => p.type === 'group')?.value ?? ',',
    decimal: parts.find((p) => p.type === 'decimal')?.value ?? '.',
  }
}

/**
 * Parses what a person typed into minor units, or null when it is not a valid amount.
 * Accepts the locale's separators ("1.234,50" in tr-TR, "1,234.50" in en-US), a currency
 * symbol, and a lone alternate separator followed by a non-triple digit run ("1.5" in tr-TR).
 */
export function parseMoneyInput(text: string, currency: string, locale: string): bigint | null {
  const digits = currencyDigits(currency)
  const { group, decimal } = separators(locale)
  let s = text.replace(/[\s\u00a0\u202f]/g, '').replace(/[^\d.,'’-]/g, '')
  if (!s || s === '-') return null
  const negative = s.startsWith('-')
  s = s.replace(/-/g, '').replace(/['’]/g, '')

  const lastDecimal = s.lastIndexOf(decimal)
  let whole: string
  let frac = ''
  if (lastDecimal >= 0) {
    whole = s.slice(0, lastDecimal).split(group).join('')
    frac = s.slice(lastDecimal + 1)
    if (frac.includes(group)) return null
  } else if (s.includes(group)) {
    const parts = s.split(group)
    const tail = parts[parts.length - 1] ?? ''
    // single group char followed by 1-2 digits (not 3) reads as a decimal separator
    if (parts.length === 2 && tail.length > 0 && tail.length !== 3 && digits > 0) {
      whole = parts[0] ?? ''
      frac = tail
    } else {
      whole = parts.join('')
    }
  } else {
    whole = s
  }
  if (!/^\d*$/.test(whole) || !/^\d*$/.test(frac)) return null
  if (whole === '' && frac === '') return null
  if (frac.length > digits) return null
  const minor = BigInt((whole || '0') + frac.padEnd(digits, '0'))
  return negative ? -minor : minor
}

/** Display text for an input field: no currency symbol, no grouping noise while editing. */
export function formatMoneyPlain(minor: Minor, currency: string, locale: string): string {
  const digits = currencyDigits(currency)
  return new Intl.NumberFormat(locale, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
    useGrouping: false,
  }).format(minorToDecimalString(minor, digits) as unknown as number)
}
