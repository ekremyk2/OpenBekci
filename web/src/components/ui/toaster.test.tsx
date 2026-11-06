import { afterEach, describe, expect, it } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { clearToasts, toast } from '@/lib/toast'
import { Toaster } from './toaster'

afterEach(() => act(() => clearToasts()))

describe('Toaster', () => {
  it('shows toasts without injecting any <style> element (strict style-src CSP)', async () => {
    const before = document.querySelectorAll('style').length
    render(
      <I18nProvider i18n={i18n}>
        <Toaster />
      </I18nProvider>,
    )
    act(() => toast.warning('Too many requests'))
    expect(await screen.findByText('Too many requests')).toBeInTheDocument()
    expect(document.querySelectorAll('style').length).toBe(before)
  })
})
