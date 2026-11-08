/**
 * After a server upgrade a cached SPA may request hashed chunks that no longer exist. Vite emits
 * `vite:preloadError` for those; reload once (guarded by sessionStorage so a genuinely broken
 * deploy cannot loop) to pick up the new index.html (web plan §14).
 */
const KEY = 'ob.chunkReloadAt'
const WINDOW_MS = 10_000

export function shouldReload(now: number, last: number | null): boolean {
  return last === null || now - last > WINDOW_MS
}

export function installChunkLoadRecovery(target: Window = window) {
  const reload = () => {
    let last: number | null = null
    try {
      const v = target.sessionStorage.getItem(KEY)
      last = v ? Number(v) : null
    } catch {
      /* storage blocked: allow one reload */
    }
    const now = Date.now()
    if (!shouldReload(now, last)) return
    try {
      target.sessionStorage.setItem(KEY, String(now))
    } catch {
      /* ignore */
    }
    target.location.reload()
  }

  target.addEventListener('vite:preloadError', (event) => {
    event.preventDefault()
    reload()
  })
  target.addEventListener('unhandledrejection', (event) => {
    const msg = String((event.reason as { message?: string } | undefined)?.message ?? event.reason)
    if (
      /Failed to fetch dynamically imported module|Importing a module script failed|error loading dynamically imported module/i.test(
        msg,
      )
    ) {
      event.preventDefault()
      reload()
    }
  })
}
