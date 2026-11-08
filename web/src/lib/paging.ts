/** A cursor-paged list response (`items` plus the cursor of the next page, `null` at the end). */
export interface CursorPage<T> {
  items: T[]
  next_cursor?: string | null
}

/** Upper bound for `fetchAllPages`: 200 pages of 200 rows is far beyond any real site. */
export const MAX_PAGES = 200

export class TooManyPagesError extends Error {
  constructor(pages: number) {
    super(`gave up after ${pages} pages`)
    this.name = 'TooManyPagesError'
  }
}

/**
 * Follows the cursor until it is exhausted and returns the first page's envelope (totals, flags)
 * with every item merged in. Never truncates silently: if the bound is hit it throws, so the
 * caller shows an error instead of a partial list. `onProgress` receives the item count so far.
 */
export async function fetchAllPages<T, R extends CursorPage<T>>(
  fetchPage: (cursor: string | undefined) => Promise<R>,
  opts: { onProgress?: (loaded: number) => void; maxPages?: number } = {},
): Promise<R> {
  const maxPages = opts.maxPages ?? MAX_PAGES
  let first: R | undefined
  const items: T[] = []
  let cursor: string | undefined
  for (let page = 0; page < maxPages; page++) {
    const res = await fetchPage(cursor)
    first ??= res
    items.push(...res.items)
    opts.onProgress?.(items.length)
    if (!res.next_cursor) return { ...first, items, next_cursor: null }
    cursor = res.next_cursor
  }
  throw new TooManyPagesError(maxPages)
}
