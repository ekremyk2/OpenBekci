import { describe, expect, it } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { createQueryClient } from './providers'
import { InstanceEffects } from './InstanceEffects'

describe('InstanceEffects', () => {
  it('applies the discovery brand colour and instance name to the document', async () => {
    document.title = 'x'
    render(
      <QueryClientProvider client={createQueryClient()}>
        <InstanceEffects />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(document.title).toBe('Test Instance'))
    expect(document.documentElement.style.getPropertyValue('--brand-seed')).toBe('#0f766e')
    expect(document.documentElement.style.getPropertyValue('--brand-seed-foreground')).toBe(
      '#ffffff',
    )
  })

  it('titles differ per page: the main h1 wins over the header wordmark', async () => {
    document.body.innerHTML =
      '<header><h1>Test Instance</h1></header><main><h1 id="p">Home</h1></main>'
    render(
      <QueryClientProvider client={createQueryClient()}>
        <InstanceEffects />
      </QueryClientProvider>,
    )
    const titles: string[] = []
    for (const name of ['Home', 'Money', 'Tickets']) {
      document.getElementById('p')!.textContent = name
      await waitFor(() => expect(document.title).toBe(`${name} · Test Instance`))
      titles.push(document.title)
    }
    expect(new Set(titles).size).toBe(3)
  })
})
