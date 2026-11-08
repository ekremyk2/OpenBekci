import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from '@tanstack/react-router'
import './index.css'
import { initialLocale } from '@/app/locale'
import { Providers, createQueryClient } from '@/app/providers'
import { createAppRouter } from '@/app/router'
import { applyDocumentLocale, loadLocale } from '@/lib/i18n'
import { installChunkLoadRecovery } from '@/lib/chunk-recovery'

installChunkLoadRecovery()

async function bootstrap() {
  if (import.meta.env.MODE === 'mock' && !import.meta.env.PROD) {
    // MSW answers /v1 in the browser (pnpm dev:mock); dead code in production builds
    await (await import('./mocks/browser')).startMocks()
  }
  const locale = initialLocale()
  await loadLocale(locale) // catalog first so the first paint is already translated
  applyDocumentLocale(locale)

  const queryClient = createQueryClient()
  const router = createAppRouter(queryClient)

  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <Providers queryClient={queryClient} locale={locale}>
        <RouterProvider router={router} />
      </Providers>
    </StrictMode>,
  )
}

void bootstrap()
