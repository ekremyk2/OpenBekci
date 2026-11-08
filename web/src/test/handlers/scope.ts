import { registerReset } from '../reset'

/**
 * Several mock models answer the same endpoints with their own seed (units, persons, files, ...):
 * the money model has its own site A1 to B3, the governance model its own owners. Where they
 * overlap, the model of the screen on display answers first and the shared tenancy, people and
 * operations models answer otherwise. A scope is "on" while a test of that module is mounted
 * (`force()`) or, in the browser mock, while the page URL contains one of `pages`.
 */
export function createScope(pages: readonly string[]) {
  let forced = false
  registerReset(() => {
    forced = false
  })
  return {
    force() {
      forced = true
    },
    release() {
      forced = false
    },
    has(request: Request): boolean {
      if (forced) return true
      try {
        const path = new URL(request.referrer).pathname
        return pages.some((p) => path.includes(p))
      } catch {
        return false
      }
    },
  }
}
