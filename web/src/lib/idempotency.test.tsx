import { describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useBodyIdempotencyKey, useIdempotencyKey, withIdempotencyKey } from './idempotency'

describe('useIdempotencyKey', () => {
  it('keeps one key for the lifetime of the form, across re-renders and retries', () => {
    const { result, rerender } = renderHook(() => useIdempotencyKey())
    const first = result.current.key
    expect(first).toMatch(/^[0-9a-f-]{36}$/)
    rerender()
    rerender()
    expect(result.current.key).toBe(first)
  })

  it('gives each form instance its own key and a new one after renew()', () => {
    const a = renderHook(() => useIdempotencyKey())
    const b = renderHook(() => useIdempotencyKey())
    expect(a.result.current.key).not.toBe(b.result.current.key)
    const before = a.result.current.key
    act(() => a.result.current.renew())
    expect(a.result.current.key).not.toBe(before)
  })

  it('builds request options with the header', () => {
    expect(withIdempotencyKey('k')).toEqual({ headers: { 'Idempotency-Key': 'k' } })
  })
})

describe('useBodyIdempotencyKey', () => {
  it('keeps the key while the body is unchanged and renews it as soon as the body changes', () => {
    const { result, rerender } = renderHook(({ body }) => useBodyIdempotencyKey(body), {
      initialProps: { body: { a: 1 } as unknown },
    })
    const first = result.current.key
    rerender({ body: { a: 1 } })
    expect(result.current.key).toBe(first)
    rerender({ body: { a: 2 } })
    const second = result.current.key
    expect(second).not.toBe(first)
    rerender({ body: { a: 2 } })
    expect(result.current.key).toBe(second)
    act(() => result.current.renew())
    expect(result.current.key).not.toBe(second)
  })
})
