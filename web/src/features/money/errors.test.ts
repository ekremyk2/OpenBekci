import { describe, expect, it } from 'vitest'
import { AppError } from '@/api/errors'
import { moneyErrorText } from './labels'

describe('moneyErrorText', () => {
  it('maps field issues, codes and known 409 reasons, else the fallback', () => {
    const field = new AppError({
      status: 422,
      code: 'validation_failed',
      fieldErrors: { from: [{ code: 'must_not_be_in_the_past' }] },
    })
    expect(moneyErrorText(field, 'x')).toBe('The start date cannot be in the past.')
    const code = new AppError({ status: 409, code: 'payment_provider_unavailable' })
    expect(moneyErrorText(code, 'x')).toBe('Card payment is not available for this site right now.')
    const conflict = new AppError({
      status: 409,
      code: 'conflict',
      message: 'the waiver is larger than the unpaid late fee',
    })
    expect(moneyErrorText(conflict, 'x')).toBe('The waiver is more than the unpaid late fee.')
    expect(moneyErrorText(new AppError({ status: 409, code: 'conflict' }), 'x')).toBe('x')
  })
})
