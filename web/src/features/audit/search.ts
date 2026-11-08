// Search-parameter shape of the audit log (see features/tickets/search.ts for why it is separate).
export interface AuditSearch {
  action?: string
  target?: string
  actor?: string
  from?: string
  to?: string
}

export function parseAuditSearch(raw: Record<string, unknown>): AuditSearch {
  const s = (v: unknown) => (typeof v === 'string' && v ? v : undefined)
  return {
    action: s(raw.action),
    target: s(raw.target),
    actor: s(raw.actor),
    from: s(raw.from),
    to: s(raw.to),
  }
}
