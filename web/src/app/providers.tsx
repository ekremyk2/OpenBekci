import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AppError } from '@/api/errors'
import { LocaleProvider } from './locale'
import type { Locale } from '@/lib/i18n'
import { Toaster } from '@/components/ui/sonner'

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        refetchOnWindowFocus: true,
        retry: (count, error) =>
          !(error instanceof AppError && error.status >= 400 && error.status < 500) && count < 2,
      },
    },
  })
}

export function Providers({
  queryClient,
  locale,
  children,
}: {
  queryClient: QueryClient
  locale: Locale
  children: React.ReactNode
}) {
  return (
    <QueryClientProvider client={queryClient}>
      <LocaleProvider initial={locale}>
        {children}
        <Toaster position="bottom-center" />
      </LocaleProvider>
    </QueryClientProvider>
  )
}
