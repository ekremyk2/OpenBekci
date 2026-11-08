import { describe, expect, it } from 'vitest'
import { i18n } from '@lingui/core'
import { occupancyKindLabel, problemMessage, roleLabel } from './labels'

const t = i18n

describe('labels', () => {
  it('localizes known roles and occupancy kinds, falling back to the raw id', () => {
    expect(roleLabel('site_manager', t)).toBe('Site manager')
    expect(roleLabel('brand_new_role', t)).toBe('brand_new_role')
    expect(occupancyKindLabel('tenant', t)).toBe('Tenant')
    expect(occupancyKindLabel('subtenant', t)).toBe('subtenant')
  })

  it('has user-facing messages for the idempotency and locked-period problems', () => {
    expect(problemMessage('idempotency_mismatch', t)).toMatch(/different details/)
    expect(problemMessage('locked_period', t)).toMatch(/closed/)
    expect(problemMessage('something_else', t)).toBeUndefined()
  })
})
