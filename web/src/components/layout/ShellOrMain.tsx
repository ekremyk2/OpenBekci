import { createContext, lazy, Suspense, useContext } from 'react'
import { useSession } from '@/app/session'

export const ShellContext = createContext(false)

// lazy: the shell is a route-level chunk; the entry bundle only needs it for 404 and error pages
const AppShell = lazy(() => import('./AppShell').then((m) => ({ default: m.AppShell })))

/** Pages that can render with or without the shell (404, platform, denial): shell when signed in. */
export function ShellOrMain({ children }: { children: React.ReactNode }) {
  const { me } = useSession()
  if (useContext(ShellContext)) return <>{children}</>
  if (me) {
    return (
      <Suspense fallback={null}>
        <AppShell>{children}</AppShell>
      </Suspense>
    )
  }
  return (
    <main id="main" className="min-h-dvh">
      {children}
    </main>
  )
}
