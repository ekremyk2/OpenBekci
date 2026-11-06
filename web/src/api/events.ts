import type { AppError } from './errors'

/**
 * The API client reports cross-cutting conditions here; UI components (see
 * app/ErrorEffects.tsx) subscribe. This keeps api/ free of React and router imports.
 */
export type ApiEvent =
  | { type: 'unauthorized'; error: AppError }
  | { type: 'version-conflict'; error: AppError }
  | { type: 'rate-limited'; error: AppError }
  | { type: 'reauth-required'; error: AppError }

type Listener = (event: ApiEvent) => void
const listeners = new Set<Listener>()

export function onApiEvent(listener: Listener): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function emitApiEvent(event: ApiEvent) {
  for (const l of listeners) l(event)
}
