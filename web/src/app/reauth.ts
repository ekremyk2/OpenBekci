import { useSyncExternalStore } from 'react'
import { AppError } from '@/api/errors'

/*
 * Re-authentication coordination (web plan §7). A sensitive request answers 403 `reauth_required`;
 * `runWithReauth` catches it, asks the user through the shared `ReauthDialog`, and replays the
 * same closure, so the replay carries the same Idempotency-Key as the first attempt.
 */

interface State {
  open: boolean
}
let state: State = { open: false }
let waiters: { resolve: () => void; reject: (e: Error) => void }[] = []
let replays = 0
const listeners = new Set<() => void>()
const emit = () => listeners.forEach((l) => l())

/** Opens the dialog (or joins the open one). Resolves once the session is re-authenticated. */
export function requestReauth(): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    waiters.push({ resolve, reject })
    if (!state.open) {
      state = { open: true }
      emit()
    }
  })
}

/** Called by the dialog. */
export function settleReauth(ok: boolean) {
  const current = waiters
  waiters = []
  state = { open: false }
  emit()
  for (const w of current) {
    if (ok) w.resolve()
    else w.reject(new Error('reauth_cancelled'))
  }
}

export function useReauthOpen(): boolean {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    () => state.open,
    () => false,
  )
}

/** True while some caller of `runWithReauth` will replay its request after a successful re-auth. */
export function hasPendingReplays(): boolean {
  return replays > 0
}

/**
 * Runs `fn(key)`; `key` is created once and is identical on the replay (use it as the
 * Idempotency-Key of operations that have no form-level key). On 403 `reauth_required` shows the re-authentication dialog and, once confirmed,
 * runs `fn` once more. Cancelling rethrows the original error.
 */
export async function runWithReauth<T>(fn: (key: string) => Promise<T>): Promise<T> {
  // One Idempotency-Key per logical operation: the first attempt and the replay receive the same
  // key, so a request that did reach the server (or whose response was lost) is deduplicated.
  const key = crypto.randomUUID()
  try {
    return await fn(key)
  } catch (e) {
    if (!(e instanceof AppError && e.isReauthRequired)) throw e
    replays++
    try {
      await requestReauth()
    } catch {
      throw e
    } finally {
      replays--
    }
    return fn(key)
  }
}
