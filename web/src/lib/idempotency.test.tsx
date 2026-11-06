import { describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useIdempotencyKey, withIdempotencyKey } from './idempotency'

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
