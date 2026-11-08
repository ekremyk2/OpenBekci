import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import MarkdownView from './Markdown'

describe('MarkdownView', () => {
  it('renders javascript: and data: links inert (text only, no href)', () => {
    const { container } = render(
      <MarkdownView
        source={
          '[click me](javascript:alert(1)) and [data](data:text/html,<b>x</b>) and [ok](https://example.org/a)'
        }
      />,
    )
    const links = [...container.querySelectorAll('a')]
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['https://example.org/a'])
    expect(container.innerHTML).not.toMatch(/javascript:/i)
    expect(container.textContent).toContain('click me')
    expect(container.textContent).toContain('data')
  })

  it('opens real links safely', () => {
    const { container } = render(<MarkdownView source="[ok](https://example.org/a)" />)
    const a = container.querySelector('a')!
    expect(a).toHaveAttribute('target', '_blank')
    expect(a.getAttribute('rel')).toBe('noopener noreferrer nofollow')
  })

  it('drops images and raw HTML', () => {
    const { container } = render(
      <MarkdownView
        source={
          '![track](https://evil.example/pixel.png) <img src=x onerror=alert(1)> <script>x</script>'
        }
      />,
    )
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('script')).toBeNull()
    expect(container.innerHTML).not.toMatch(/onerror/i)
  })
})
