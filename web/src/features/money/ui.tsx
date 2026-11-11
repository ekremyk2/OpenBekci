import { useMemo, useState } from 'react'
import type { UseQueryResult } from '@tanstack/react-query'
import { DownloadIcon, LockIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { FieldValues, Path, UseFormReturn } from 'react-hook-form'
import { AppError } from '@/api/errors'
import { useLocale } from '@/app/locale'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton, TableSkeleton } from '@/components/data/Skeletons'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import {
  downloadFile,
  useMoneyCtx,
  type DownloadEndpoint,
  type ExportFormat,
  type MoneyCtx,
} from './api'

export { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
export { Textarea }

/** The shared checkbox with its text label (the checkbox itself lives in `components/ui`). */
export function LabeledCheckbox({
  label,
  className,
  ...props
}: Omit<React.ComponentProps<'input'>, 'type'> & { label: React.ReactNode }) {
  return (
    <label className={cn('flex items-center gap-2 text-sm', className)}>
      <Checkbox {...props} />
      {label}
    </label>
  )
}

/** Label + control + inline error (field-level server and client errors share this slot). */
export function Field({
  label,
  htmlFor,
  error,
  hint,
  className,
  children,
}: {
  label: React.ReactNode
  htmlFor?: string
  error?: string
  hint?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  )
}

/** Month as `YYYY-MM` in the app language (the native month input follows the browser locale instead). */
export function MonthPicker({
  id,
  value,
  onChange,
}: {
  id?: string
  value: string
  onChange: (v: string) => void
}) {
  const { intl } = useLocale()
  const { t } = useLingui()
  const [y, m] = value.split('-')
  const names = useMemo(() => {
    const f = new Intl.DateTimeFormat(intl, { month: 'long', timeZone: 'UTC' })
    return Array.from({ length: 12 }, (_, i) => f.format(Date.UTC(2000, i, 1)))
  }, [intl])
  const year = Number(y) || new Date().getFullYear()
  return (
    <div className="flex gap-2">
      <Select
        id={id}
        aria-label={t`Month`}
        value={m ?? ''}
        onChange={(e) => onChange(`${year}-${e.target.value}`)}
      >
        {names.map((n, i) => (
          <option key={n} value={String(i + 1).padStart(2, '0')}>
            {n}
          </option>
        ))}
      </Select>
      <Select
        aria-label={t`Year`}
        value={String(year)}
        onChange={(e) => onChange(`${e.target.value}-${m ?? '01'}`)}
      >
        {Array.from({ length: 12 }, (_, i) => year - 8 + i).map((yy) => (
          <option key={yy} value={yy}>
            {yy}
          </option>
        ))}
      </Select>
    </div>
  )
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
}) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold">{title}</h1>
        {description ? (
          <p className="max-w-prose text-sm text-muted-foreground">{description}</p>
        ) : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  )
}

/** No permission for this screen (403 is also mapped by ErrorState). */
export function NoPermission() {
  const { t } = useLingui()
  return (
    <EmptyState
      icon={LockIcon}
      title={t`You do not have access to this`}
      description={t`Ask your manager if you think you should.`}
    />
  )
}

/** Renders children only when the session grants `permission` in the current organization. */
export function Gate({
  permission,
  anyOf,
  fallback,
  children,
}: {
  permission?: string
  anyOf?: string[]
  fallback?: React.ReactNode
  children: React.ReactNode
}) {
  const { can } = useSession()
  const list = anyOf ?? (permission ? [permission] : [])
  if (list.length > 0 && !list.some((p) => can(p))) return <>{fallback ?? <NoPermission />}</>
  return <>{children}</>
}

/** Loading / error / empty / data states of a query in one place. */
export function QueryBoundary<T>({
  query,
  isEmpty,
  emptyTitle,
  emptyDescription,
  emptyAction,
  skeleton = 'table',
  children,
}: {
  query: Pick<UseQueryResult<T>, 'isPending' | 'isError' | 'error' | 'refetch'> & {
    data: T | undefined
  }
  isEmpty?: (data: T) => boolean
  emptyTitle?: React.ReactNode
  emptyDescription?: React.ReactNode
  emptyAction?: React.ReactNode
  skeleton?: 'table' | 'page'
  children: (data: T) => React.ReactNode
}) {
  if (query.isPending) return skeleton === 'page' ? <PageSkeleton /> : <TableSkeleton />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  if (isEmpty?.(query.data as T)) {
    return (
      <EmptyState
        title={emptyTitle ?? <Trans>Nothing here yet</Trans>}
        description={emptyDescription}
        action={emptyAction}
      />
    )
  }
  return <>{children(query.data as T)}</>
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

const STATUS_VARIANT: Record<string, 'success' | 'warning' | 'destructive' | 'info' | 'secondary'> =
  {
    paid: 'success',
    confirmed: 'success',
    matched: 'success',
    ready: 'success',
    posted: 'success',
    active: 'success',
    approved: 'success',
    final: 'success',
    open: 'info',
    partially_paid: 'warning',
    pending_declared: 'warning',
    suggested: 'info',
    partial: 'warning',
    unmatched: 'warning',
    cancelled: 'secondary',
    voided: 'secondary',
    refunded: 'secondary',
    ignored: 'secondary',
    closed: 'secondary',
    ended: 'secondary',
    archived: 'secondary',
    paused: 'secondary',
    draft: 'secondary',
    failed: 'destructive',
    parsing: 'info',
    pending: 'warning',
    expired: 'secondary',
  }

export function StatusBadge({ status }: { status: string }) {
  const { t } = useLingui()
  const label: Record<string, string> = {
    paid: t`Paid`,
    confirmed: t`Confirmed`,
    matched: t`Matched`,
    ready: t`Ready`,
    posted: t`Posted`,
    active: t`Active`,
    approved: t`Approved`,
    final: t`Final`,
    open: t`Open`,
    partially_paid: t`Partially paid`,
    pending_declared: t`Awaiting confirmation`,
    suggested: t`Suggested`,
    partial: t`Partial`,
    unmatched: t`Unmatched`,
    cancelled: t`Cancelled`,
    voided: t`Voided`,
    refunded: t`Refunded`,
    ignored: t`Ignored`,
    closed: t`Closed`,
    ended: t`Ended`,
    archived: t`Archived`,
    paused: t`Paused`,
    draft: t`Draft`,
    failed: t`Failed`,
    parsing: t`Parsing`,
    pending: t`Pending`,
    expired: t`Expired`,
  }
  return <Badge variant={STATUS_VARIANT[status] ?? 'secondary'}>{label[status] ?? status}</Badge>
}

export function SideBadge({ side }: { side: string | null | undefined }) {
  const { t } = useLingui()
  if (!side) return null
  return <Badge variant="outline">{side === 'tenant' ? t`Tenant` : t`Owner`}</Badge>
}

/** Maps an AppError onto react-hook-form: field errors inline, everything else as a root error. */
export function applyServerErrors<T extends FieldValues>(
  form: UseFormReturn<T>,
  error: unknown,
  fallback: string,
  known?: Record<string, string>,
) {
  if (error instanceof AppError) {
    if (error.isValidation && Object.keys(error.fieldErrors).length > 0) {
      for (const [field, issues] of Object.entries(error.fieldErrors)) {
        form.setError(field as Path<T>, {
          message: issues[0]?.message ?? issues[0]?.code ?? fallback,
        })
      }
      return
    }
    const mapped = known?.[error.code]
    form.setError('root', { message: mapped ?? error.message ?? fallback })
    return
  }
  form.setError('root', { message: fallback })
}

export function FormRootError({ message }: { message?: string }) {
  if (!message) return null
  return (
    <p role="alert" className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
      {message}
    </p>
  )
}

/** CSV / XLSX (and PDF) export buttons for a report or statement endpoint. */
export function ExportButtons({
  endpoint,
  pathParams,
  query,
  basename,
  formats = ['csv', 'xlsx'],
}: {
  endpoint: DownloadEndpoint
  /** Path parameters other than `org_id`, which comes from the current organization. */
  pathParams?: Record<string, string>
  query: Record<string, string | undefined>
  basename: string
  formats?: ExportFormat[]
}) {
  const { t } = useLingui()
  const ctx = useMoneyCtx()!
  const [busy, setBusy] = useState<ExportFormat | null>(null)
  async function go(f: ExportFormat) {
    setBusy(f)
    try {
      await downloadFile(
        { endpoint, path: { org_id: ctx.orgId, ...pathParams }, query, format: f },
        `${basename}.${f}`,
      )
    } catch {
      toast.error(t`The download failed. Please try again.`)
    } finally {
      setBusy(null)
    }
  }
  return (
    <div className="flex gap-2">
      {formats.map((f) => (
        <Button
          key={f}
          variant="outline"
          size="sm"
          disabled={busy !== null}
          onClick={() => void go(f)}
        >
          <DownloadIcon />
          {f.toUpperCase()}
        </Button>
      ))}
    </div>
  )
}

/**
 * Asks for a written reason (cancel, void, waive, reverse, reject). The reason is part of the
 * audit trail; `children` can show what the action will do ("a reversal is created").
 */
export function ReasonDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  destructive,
  pending,
  error,
  onConfirm,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  description?: React.ReactNode
  confirmLabel: React.ReactNode
  destructive?: boolean
  pending?: boolean
  error?: string
  onConfirm: (reason: string) => void
  children?: React.ReactNode
}) {
  const { t } = useLingui()
  const [reason, setReason] = useState('')
  const [touched, setTouched] = useState(false)
  const missing = touched && reason.trim() === ''
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) {
          setReason('')
          setTouched(false)
        }
        onOpenChange(o)
      }}
    >
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            setTouched(true)
            if (reason.trim() !== '') onConfirm(reason.trim())
          }}
        >
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            {description ? <DialogDescription>{description}</DialogDescription> : null}
          </DialogHeader>
          {children}
          <Field
            label={t`Reason`}
            htmlFor="reason-input"
            error={missing ? t`A reason is required.` : undefined}
          >
            <Textarea
              id="reason-input"
              value={reason}
              aria-invalid={missing || undefined}
              onChange={(e) => setReason(e.target.value)}
            />
          </Field>
          <FormRootError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              <Trans>Cancel</Trans>
            </Button>
            <Button
              type="submit"
              variant={destructive ? 'destructive' : 'default'}
              disabled={pending}
            >
              {confirmLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Plain text input wired for a native date (calendar date, no timezone). */
export function DateField(props: Omit<React.ComponentProps<typeof Input>, 'type'>) {
  return <Input type="date" dir="ltr" {...props} />
}

export function SitePrefix({ ctx }: { ctx: MoneyCtx }) {
  return <span className="text-xs text-muted-foreground">{ctx.siteName}</span>
}
