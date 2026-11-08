import { describe, expect, it } from 'vitest'
import { normalizeDestination, normalizeE164 } from './contact'

describe('invitation destinations', () => {
  it('normalizes E.164 phone numbers and rejects others', () => {
    expect(normalizeE164('+90 555 111 22 33')).toBe('+905551112233')
    expect(normalizeE164('0555 111 22 33')).toBeNull()
    expect(normalizeE164('+0123456789')).toBeNull()
    expect(normalizeE164('abc')).toBeNull()
  })
  it('checks the destination against the channel', () => {
    expect(normalizeDestination('sms', '+905551112233')).toBe('+905551112233')
    expect(normalizeDestination('sms', 'a@b.co')).toBeNull()
    expect(normalizeDestination('email', ' a@b.co ')).toBe('a@b.co')
    expect(normalizeDestination('email', '+905551112233')).toBeNull()
  })
})
