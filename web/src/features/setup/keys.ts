import { queryOptions } from '@tanstack/react-query'
import { draftApi } from '@/api/draft'
import { separators } from '@/lib/money'
import { fetchAllPages } from '@/lib/paging'

export const keyQueries = {
  list: (orgId: string, siteId: string) =>
    queryOptions({
      queryKey: ['tenancy.listAllocationKeys', orgId, siteId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/allocation-keys', {
          params: { path: { org_id: orgId }, query: { site_id: siteId, limit: 100 } },
        })
        return data!
      },
      staleTime: 5 * 60_000,
    }),
  /** Every value of a key (cursor followed to the end, never truncated). */
  values: (orgId: string, keyId: string, onProgress?: (loaded: number) => void) =>
    queryOptions({
      queryKey: ['tenancy.listAllocationKeyValues', orgId, keyId],
      queryFn: () =>
        fetchAllPages(
          async (cursor) => {
            const { data } = await draftApi.GET(
              '/v1/orgs/{org_id}/allocation-keys/{key_id}/values',
              { params: { path: { org_id: orgId, key_id: keyId }, query: { cursor, limit: 200 } } },
            )
            return data!
          },
          { onProgress },
        ),
    }),
}

/**
 * Turns pasted spreadsheet text into `[door, value]` or `[value]` rows. Cells split on tab (what
 * Excel and Sheets copy) or semicolon; decimal commas are accepted.
 */
export function parsePasted(text: string): { door: string | null; value: string }[] {
  return text
    .split(/\r?\n/)
    .filter((l) => l.trim() !== '')
    .map((line) => {
      const cells = line.split(/\t|;/).map((c) => c.trim())
      return cells.length >= 2
        ? { door: cells[0] ?? null, value: cells[1] ?? '' }
        : { door: null, value: cells[0] ?? '' }
    })
}

/**
 * Parses a weight the way the active locale writes numbers. Unambiguous input is accepted
 * whichever convention it uses ("1.234,5" and "1,234.5" are both 1234.5, because the separator
 * that comes last is the decimal one). A lone separator is read with the locale's meaning
 * ("1,5" is 1.5 in tr, "1.234" is 1234 in tr and 1.234 in en). Input that could be read two ways
 * or that is not a number ("1,5" in en, "1234.5" in tr, "1/3") gives NaN, never a guess.
 */
export function parseWeight(text: string, locale = 'en'): number {
  const s = text.replace(/[\s\u00a0\u202f]/g, '')
  if (!/^-?[\d.,]+$/.test(s)) return NaN
  const negative = s.startsWith('-')
  const body = negative ? s.slice(1) : s
  const { group, decimal } = separators(locale)
  const grouped = (sep: string) => new RegExp(`^\\d{1,3}(\\${sep}\\d{3})+$`).test(body)
  const digitsOnly = (v: string) => /^\d+$/.test(v)
  let normalized: string | null = null

  const lastDot = body.lastIndexOf('.')
  const lastComma = body.lastIndexOf(',')
  if (lastDot >= 0 && lastComma >= 0) {
    // both present: the one that comes last is the decimal separator, the other groups thousands
    const dec = lastDot > lastComma ? '.' : ','
    const grp = dec === '.' ? ',' : '.'
    const at = body.lastIndexOf(dec)
    const whole = body.slice(0, at)
    const frac = body.slice(at + 1)
    if (digitsOnly(frac)) {
      if (new RegExp(`^\\d{1,3}(\\${grp}\\d{3})+$`).test(whole)) {
        normalized = `${whole.split(grp).join('')}.${frac}`
      }
    }
  } else if (lastDot >= 0 || lastComma >= 0) {
    const sep = lastDot >= 0 ? '.' : ','
    const count = body.split(sep).length - 1
    if (count > 1) {
      // repeated: only thousands grouping makes sense ("1.234.567")
      if (grouped(sep)) normalized = body.split(sep).join('')
    } else if (sep === decimal) {
      const [whole = '', frac = ''] = body.split(sep)
      if ((whole === '' || digitsOnly(whole)) && digitsOnly(frac))
        normalized = `${whole || '0'}.${frac}`
    } else if (sep === group) {
      if (grouped(sep)) normalized = body.split(sep).join('')
    }
  } else if (digitsOnly(body)) {
    normalized = body
  }
  if (normalized === null) return NaN
  const n = Number(normalized)
  return Number.isFinite(n) ? (negative ? -n : n) : NaN
}

/** A weight as it is shown in an input: the locale's decimal separator, no grouping. */
export function formatWeight(n: number, locale = 'en'): string {
  let nf: Intl.NumberFormat
  try {
    nf = new Intl.NumberFormat(locale, { useGrouping: false, maximumFractionDigits: 6 })
  } catch {
    nf = new Intl.NumberFormat('en', { useGrouping: false, maximumFractionDigits: 6 })
  }
  return nf.format(n)
}
