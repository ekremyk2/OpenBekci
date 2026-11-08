/**
 * Per-test reset hooks. A feature's mock model registers its own reset next to the model
 * (`registerReset(resetMyDb)`), and `setup.ts` runs them all after every test, so the shared setup
 * file never has to import one feature's handlers.
 */
const resets: (() => void)[] = []

export function registerReset(fn: () => void) {
  resets.push(fn)
}

export function runResets() {
  for (const fn of resets) fn()
}
