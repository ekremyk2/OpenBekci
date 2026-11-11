import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { DownloadIcon } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { Button } from '@/components/ui/button'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { downloadFile, type DownloadRequest } from '@/api/download'

/** Downloads a PDF (or any file) from an authenticated endpoint. */
export function DownloadButton({
  request,
  filename,
  children,
  variant = 'outline',
  size = 'sm',
  disabled,
}: {
  request: DownloadRequest
  filename: string
  children: React.ReactNode
  variant?: 'outline' | 'default' | 'secondary' | 'ghost'
  size?: 'sm' | 'default' | 'lg'
  disabled?: boolean
}) {
  const { t } = useLingui()
  const [busy, setBusy] = useState(false)
  async function go() {
    setBusy(true)
    try {
      await downloadFile(request, filename)
    } catch {
      toast.error(t`The download failed. Please try again.`)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Button variant={variant} size={size} disabled={busy || disabled} onClick={() => void go()}>
      <DownloadIcon />
      {children}
    </Button>
  )
}

/** A short boxed message: tone decides the colour. */
export function Callout({
  tone = 'info',
  children,
  className,
  role,
}: {
  tone?: 'info' | 'warning' | 'destructive' | 'success'
  children: React.ReactNode
  className?: string
  role?: 'alert' | 'status'
}) {
  const toneClass = {
    info: 'border-info/40 bg-info/10',
    warning: 'border-warning/40 bg-warning/10',
    destructive: 'border-destructive/40 bg-destructive/10',
    success: 'border-success/40 bg-success/10',
  }[tone]
  return (
    <div role={role} className={cn('rounded-md border p-3 text-sm', toneClass, className)}>
      {children}
    </div>
  )
}

export function Stat({
  label,
  children,
  hint,
}: {
  label: React.ReactNode
  children: React.ReactNode
  hint?: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border bg-card p-4">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className="text-2xl font-semibold">{children}</span>
      {hint ? <span className="text-xs text-muted-foreground">{hint}</span> : null}
    </div>
  )
}

/** Tab-like link bar of a section (filtered by permission by the caller). */
export function SectionNav({
  label,
  items,
  orgId,
}: {
  label: string
  items: { to: string; label: string; exact?: boolean }[]
  orgId: string
}) {
  return (
    <nav aria-label={label} className="-mx-1 flex gap-1 overflow-x-auto px-1 pb-1">
      {items.map((tab) => (
        <Link
          key={tab.to}
          to={tab.to as '/orgs/$orgId'}
          params={{ orgId }}
          activeOptions={{ exact: !!tab.exact }}
          className={cn(
            'flex min-h-11 items-center rounded-md px-3 py-1.5 sm:min-h-9 text-sm font-medium whitespace-nowrap outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background',
            '[&.active]:bg-primary [&.active]:text-primary-foreground',
          )}
        >
          {tab.label}
        </Link>
      ))}
    </nav>
  )
}
