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
})
