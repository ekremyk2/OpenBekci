import { HttpResponse } from 'msw'

/** application/problem+json like the backend's `Problem` (api/src/components/problem.yaml). */
export function problem(
  status: number,
  code: string,
  detail?: string,
  errors?: { field: string; code: string; message?: string }[],
) {
  return HttpResponse.json(
    { type: `about:blank#${code}`, title: code, status, code, detail, errors },
    { status, headers: { 'Content-Type': 'application/problem+json' } },
  )
}

export const notFound = () => problem(404, 'not_found')

/** Cursor pagination over an in-memory array: the cursor is the next offset. */
export function paginate<T>(items: T[], url: URL, defaultLimit = 25) {
  const limit = Math.min(Number(url.searchParams.get('limit')) || defaultLimit, 200)
  const start = Number(url.searchParams.get('cursor')) || 0
  const slice = items.slice(start, start + limit)
  const end = start + limit
  return { items: slice, next_cursor: end < items.length ? String(end) : null }
}

/** Case-insensitive "contains" over several fields. */
export function matches(q: string | null, ...fields: (string | null | undefined)[]) {
  if (!q) return true
  const needle = q.toLowerCase()
  return fields.some((f) => f?.toLowerCase().includes(needle))
}

/** `If-Match: "3"` against the stored version; null when it matches (or none was sent). */
export function versionConflict(request: Request, version: number) {
  const header = request.headers.get('If-Match')
  if (header && header.replace(/"/g, '') !== String(version)) {
    return problem(409, 'version_conflict', 'The record changed since you read it.')
  }
  return null
}

let counter = 1000
export const newId = (prefix: string) => `${prefix}-${++counter}`
export const nowIso = () => new Date().toISOString()
