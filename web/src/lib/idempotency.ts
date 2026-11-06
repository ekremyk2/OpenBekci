import { useCallback, useState } from 'react'

/** Request options carrying an `Idempotency-Key`; spread into an `api.POST(...)` call. */
export function withIdempotencyKey(key: string) {
  return { headers: { 'Idempotency-Key': key } }
}

/**
 * One `Idempotency-Key` per form submission (web plan §6), not per request. The key lives as long
 * as the form instance, so a retry after a network error, a double click, and the replay after a
 * re-authentication challenge (§7) all send the same key and the server can deduplicate them.
 * Call `renew()` only once a submission has succeeded and the form stays open for a new one.
 *
 *   const idem = useIdempotencyKey()
 *   api.POST('/v1/orgs/{org_id}/payments', { body, params, ...withIdempotencyKey(idem.key) })
 */
export function useIdempotencyKey(): { key: string; renew: () => void } {
  const [key, setKey] = useState(() => crypto.randomUUID())
  const renew = useCallback(() => setKey(crypto.randomUUID()), [])
  return { key, renew }
}
