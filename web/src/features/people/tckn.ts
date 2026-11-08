/** Turkish national id (TCKN) checksum, the validator the core ships for country `TR` (S6). */
export function isValidTckn(value: string): boolean {
  if (!/^[1-9]\d{10}$/.test(value)) return false
  const d = value.split('').map(Number)
  const odd = d[0]! + d[2]! + d[4]! + d[6]! + d[8]!
  const even = d[1]! + d[3]! + d[5]! + d[7]!
  const tenth = (((odd * 7 - even) % 10) + 10) % 10
  if (tenth !== d[9]) return false
  const sum = d.slice(0, 10).reduce((s, n) => s + n, 0)
  return sum % 10 === d[10]
}

/** Validates an id for its country; unknown countries only need to be non-empty. */
export function isValidNationalId(country: string, value: string): boolean {
  const v = value.replace(/\s+/g, '')
  if (country === 'TR') return isValidTckn(v)
  return v.length >= 4
}
