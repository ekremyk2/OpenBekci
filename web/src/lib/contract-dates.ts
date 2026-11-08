/**
 * The ONE place that decides how a calendar day goes on the wire (web plan §3).
 *
 * The final contract names these fields `received_on`, `transferred_on` and `refunded_on` and types
 * them as `date`, so the calendar day goes out as it is, with no instant and no timezone shift.
 * This table is the only place that knows the wire names.
 */
const FIELDS = {
  received: 'received_on',
  transferred: 'transferred_on',
  refunded: 'refunded_on',
} as const

type Fields = typeof FIELDS
export type ContractDateField = keyof Fields

/** `{ received_on: '2026-03-05' }`, to be spread into a request body. */
export function toContractDate<K extends ContractDateField>(
  field: K,
  isoDate: string,
): { [P in Fields[K]]: string } {
  return { [FIELDS[field]]: isoDate } as { [P in Fields[K]]: string }
}
