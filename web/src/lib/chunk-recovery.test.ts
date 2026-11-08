import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { installChunkLoadRecovery, shouldReload } from './chunk-recovery'

describe('shouldReload', () => {
  it('allows the first reload and one after the guard window', () => {
    expect(shouldReload(100_000, null)).toBe(true)
    expect(shouldReload(100_000, 95_000)).toBe(false)
    expect(shouldReload(100_000, 80_000)).toBe(true)
  })
})

describe('installChunkLoadRecovery', () => {
  const reload = vi.fn()
  let target: Window

  beforeEach(() => {
    reload.mockClear()
    sessionStorage.clear()
    // a Window-like target so jsdom's non-configurable location.reload is not touched
    const t = new EventTarget() as unknown as Window
    Object.defineProperty(t, 'sessionStorage', { value: sessionStorage })
    Object.defineProperty(t, 'location', { value: { reload } })
    target = t
    installChunkLoadRecovery(target)
  })
  afterEach(() => vi.useRealTimers())

  it('reloads once on vite:preloadError and prevents the default error', () => {
    const event = new Event('vite:preloadError', { cancelable: true })
    target.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('does not loop: a second failure inside the window does not reload again', () => {
    target.dispatchEvent(new Event('vite:preloadError', { cancelable: true }))
    target.dispatchEvent(new Event('vite:preloadError', { cancelable: true }))
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('reloads again once the window has passed', () => {
    vi.useFakeTimers()
    vi.setSystemTime(1_000_000)
    target.dispatchEvent(new Event('vite:preloadError', { cancelable: true }))
    vi.setSystemTime(1_000_000 + 11_000)
    target.dispatchEvent(new Event('vite:preloadError', { cancelable: true }))
    expect(reload).toHaveBeenCalledTimes(2)
  })

  it('treats a failed dynamic import rejection as a stale chunk', () => {
    const event = new Event('unhandledrejection', { cancelable: true }) as Event & {
      reason: unknown
    }
    event.reason = new TypeError('Failed to fetch dynamically imported module: /assets/x.js')
    target.dispatchEvent(event)
    expect(reload).toHaveBeenCalledTimes(1)
    expect(event.defaultPrevented).toBe(true)
  })

  it('ignores unrelated rejections', () => {
    const event = new Event('unhandledrejection', { cancelable: true }) as Event & {
      reason: unknown
    }
    event.reason = new Error('boom')
    target.dispatchEvent(event)
    expect(reload).not.toHaveBeenCalled()
    expect(event.defaultPrevented).toBe(false)
  })
})
