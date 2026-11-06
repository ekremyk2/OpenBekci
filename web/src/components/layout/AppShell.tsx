import { useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { LogOutIcon, MenuIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { toast } from '@/lib/toast'
import { api } from '@/api/client'
import { AppError } from '@/api/errors'
import { useInstanceName } from '@/app/InstanceEffects'
import { filterNav, NAV_ITEMS } from '@/app/nav'
import { clearSession, useSession } from '@/app/session'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { cn } from '@/lib/utils'
import { OrgSwitcher } from './OrgSwitcher'
import { LocaleToggle, ThemeToggle } from './Toggles'

function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const { me, org } = useSession()
  const { i18n, t } = useLingui()
  if (!me || !org) return null
  const items = filterNav(NAV_ITEMS, { me, orgId: org.id })
  return (
    <nav aria-label={t`Main`} className="flex flex-col gap-1 p-3">
      {items.map((item) => (
        <Link
          key={item.id}
          to={item.to as '/orgs/$orgId'}
          params={{ orgId: org.id }}
          activeOptions={{ exact: item.to === '/orgs/$orgId' }}
          onClick={onNavigate}
          className={cn(
            'flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium outline-none hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring',
            '[&.active]:bg-accent [&.active]:text-accent-foreground',
          )}
        >
          <item.icon className="size-4 shrink-0" aria-hidden="true" />
          {i18n._(item.label)}
        </Link>
      ))}
    </nav>
  )
}

function Topbar({ onMenu }: { onMenu: () => void }) {
  const { t } = useLingui()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  async function signOut() {
    try {
      await api.DELETE('/v1/auth/session')
    } catch (e) {
      // 401: the session is already gone, which is what the user wanted
      if (!(e instanceof AppError && e.status === 401)) {
        toast.error(t`Could not sign out. Please try again.`)
        return
      }
    }
    clearSession(queryClient)
    await navigate({ to: '/login', search: {} })
  }

  return (
    <header className="flex h-14 items-center gap-2 border-b bg-background px-3">
      <Button
        variant="ghost"
        size="icon"
        className="md:hidden"
        onClick={onMenu}
        aria-label={t`Open menu`}
      >
        <MenuIcon />
      </Button>
      <OrgSwitcher />
      <div className="ms-auto flex items-center gap-1">
        <LocaleToggle />
        <ThemeToggle />
        <Button variant="ghost" size="icon" onClick={() => void signOut()} aria-label={t`Sign out`}>
          <LogOutIcon className="rtl-flip" />
        </Button>
      </div>
    </header>
  )
}

/** Signed-in frame: permission-filtered sidebar, topbar with org switcher and toggles. */
export function AppShell({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useState(false)
  const instanceName = useInstanceName()
  return (
    <div className="flex min-h-dvh">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:start-2 focus:top-2 focus:z-50 focus:rounded-md focus:bg-background focus:p-2"
      >
        <Trans>Skip to content</Trans>
      </a>
      <aside className="hidden w-60 shrink-0 border-e bg-sidebar text-sidebar-foreground md:block">
        <div className="flex h-14 items-center px-6 text-lg font-semibold">{instanceName}</div>
        <Sidebar />
      </aside>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent side="start" className="bg-sidebar p-0">
          <SheetHeader>
            <SheetTitle>{instanceName}</SheetTitle>
            <SheetDescription className="sr-only">
              <Trans>Navigation</Trans>
            </SheetDescription>
          </SheetHeader>
          <Sidebar onNavigate={() => setOpen(false)} />
        </SheetContent>
      </Sheet>
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar onMenu={() => setOpen(true)} />
        <main id="main" tabIndex={-1} className="flex-1 p-4 outline-none md:p-6">
          {children}
        </main>
      </div>
    </div>
  )
}
