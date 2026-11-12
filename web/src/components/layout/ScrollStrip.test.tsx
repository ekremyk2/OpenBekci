import { render, screen, fireEvent } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@lingui/react'
import { i18n } from '@lingui/core'
import { ScrollStrip } from './ScrollStrip'

it('shows a right button on overflow and scrolls on click', () => {
  i18n.loadAndActivate({ locale: 'en', messages: {} })
  Object.defineProperty(HTMLElement.prototype, 'scrollWidth', { configurable: true, value: 500 })
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, value: 100 })
  const scrollBy = vi.fn()
  HTMLElement.prototype.scrollBy = scrollBy
  render(
    <I18nProvider i18n={i18n}>
      <ScrollStrip label="x">
        <a href="#a">a</a>
      </ScrollStrip>
    </I18nProvider>,
  )
  expect(screen.getAllByRole('button')).toHaveLength(1) // start edge is at 0
  fireEvent.click(screen.getByRole('button', { name: 'Scroll right' }))
  expect(scrollBy).toHaveBeenCalledWith({ left: 80, behavior: 'smooth' })
})
