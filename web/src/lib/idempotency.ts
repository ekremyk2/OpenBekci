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

/**
 * An idempotency key bound to a request body: while the body stays the same a retry (or the replay
 * after re-authentication) sends the same key, and as soon as the body changes the key is a new one,
 * so a key is never reused with a different body (the server answers that with
 * `idempotency_mismatch`). The new key is already in place on the very render that sees the new
 * body, so the request that follows an edit never carries the old one. `renew()` still starts a new
 * key after a success.
 */
export function useBodyIdempotencyKey(body: unknown): { key: string; renew: () => void } {
  const signature = JSON.stringify(body ?? null)
  const [state, setState] = useState(() => ({ signature, key: crypto.randomUUID() }))
  let current = state
  if (state.signature !== signature) {
    // adjusting state while rendering: React re-renders at once, before any handler can run
    current = { signature, key: crypto.randomUUID() }
    setState(current)
  }
  const renew = useCallback(() => setState((s) => ({ ...s, key: crypto.randomUUID() })), [])
  return { key: current.key, renew }
}
