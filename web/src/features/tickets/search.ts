// Search-parameter shape of the ticket list. Kept apart from the page so the route file can
// validate the URL without pulling the whole screen into the entry chunk.
export type TicketView = 'all' | 'mine' | 'common' | 'queue'

export interface TicketSearch {
  view?: TicketView
  status?: string
  category?: string
  /** `p:<person id>` or `v:<vendor id>` */
  assignee?: string
  unit?: string
  visibility?: 'private' | 'common'
  q?: string
}

export function parseTicketSearch(raw: Record<string, unknown>): TicketSearch {
  const str = (v: unknown) => (typeof v === 'string' && v ? v : undefined)
  const view = str(raw.view)
  const visibility = str(raw.visibility)
  return {
    view:
      view === 'all' || view === 'mine' || view === 'common' || view === 'queue' ? view : undefined,
    status: str(raw.status),
    category: str(raw.category),
    assignee: str(raw.assignee),
    unit: str(raw.unit),
    visibility: visibility === 'private' || visibility === 'common' ? visibility : undefined,
    q: str(raw.q),
  }
}
