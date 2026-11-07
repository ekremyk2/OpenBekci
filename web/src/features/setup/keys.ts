import { queryOptions } from '@tanstack/react-query'
import { draftApi } from '@/api/draft'

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
  values: (orgId: string, keyId: string) =>
    queryOptions({
      queryKey: ['tenancy.listAllocationKeyValues', orgId, keyId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/allocation-keys/{key_id}/values', {
          params: { path: { org_id: orgId, key_id: keyId }, query: { limit: 500 } },
        })
        return data!
      },
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

/** `"1.234,5"` / `"1234.5"` / `"1,5"` → 1234.5 / 1234.5 / 1.5; NaN when it is not a number. */
export function parseWeight(text: string): number {
  const s = text.trim().replace(/\s/g, '')
  if (s === '') return NaN
  const comma = s.lastIndexOf(',')
  const dot = s.lastIndexOf('.')
  const normalized = comma > dot ? s.replace(/\./g, '').replace(',', '.') : s.replace(/,/g, '')
  return /^-?\d+(\.\d+)?$/.test(normalized) ? Number(normalized) : NaN
}
