/*
 * Audit `diff` objects (backend §4.x): ordinary fields carry their values; PII fields are never
 * sent as values but as `{ hash_before, hash_after }`. The draft contract does not pin down the
 * shape of ordinary entries, so three are understood: `{ before, after }`, `{ from, to }` and a
 * bare value ("set to"). Anything else is shown as JSON rather than dropped.
 */

export type DiffEntry =
  | { field: string; kind: 'hashed'; before: string | null; after: string | null }
  | { field: string; kind: 'changed'; before: unknown; after: unknown }
  | { field: string; kind: 'set'; value: unknown }

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

export function parseDiff(diff: unknown): DiffEntry[] {
  if (!isRecord(diff)) return []
  return Object.entries(diff).map(([field, v]): DiffEntry => {
    if (isRecord(v)) {
      if ('hash_before' in v || 'hash_after' in v) {
        return {
          field,
          kind: 'hashed',
          before: typeof v.hash_before === 'string' ? v.hash_before : null,
          after: typeof v.hash_after === 'string' ? v.hash_after : null,
        }
      }
      if ('before' in v || 'after' in v)
        return { field, kind: 'changed', before: v.before, after: v.after }
      if ('from' in v || 'to' in v) return { field, kind: 'changed', before: v.from, after: v.to }
    }
    return { field, kind: 'set', value: v }
  })
}

export function formatValue(v: unknown): string {
  if (v === null || v === undefined) return '—'
  if (typeof v === 'string') return v
  return JSON.stringify(v)
}
