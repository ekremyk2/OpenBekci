import { siteMidnight } from './dates'

/**
 * The ONE place that decides how a calendar day goes on the wire (web plan §3).
 *
 * The draft contract still types `received_at`, `transferred_at` and `refunded_at` as `date-time`
 * although they are calendar days (review of PR 13, finding 4). Until the backend renames them to
 * `received_on` / `transferred_on` / `refunded_on` (type `date`), we send a full RFC 3339 instant at
 * site-local midnight. After the rename this table and the three return types are the only edit:
 * set `wire` to the new name and `format` to `'date'`.
 */
const FIELDS = {
  received: { wire: 'received_at', format: 'date-time' },
  transferred: { wire: 'transferred_at', format: 'date-time' },
  refunded: { wire: 'refunded_at', format: 'date-time' },
} as const

type Fields = typeof FIELDS
export type ContractDateField = keyof Fields

/** `{ received_at: '2026-03-05T00:00:00+03:00' }`, to be spread into a request body. */
export function toContractDate<K extends ContractDateField>(
  field: K,
  isoDate: string,
  timeZone: string,
): { [P in Fields[K]['wire']]: string } {
  const f = FIELDS[field]
  const value = f.format === 'date-time' ? siteMidnight(isoDate, timeZone) : isoDate
  return { [f.wire]: value } as { [P in Fields[K]['wire']]: string }
}
