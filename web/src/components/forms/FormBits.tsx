import { LoaderCircleIcon } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import type { FieldValues, Path, UseFormReturn } from 'react-hook-form'
import { AppError } from '@/api/errors'
import { problemMessage } from '@/lib/labels'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'

/** Label + control + associated error text (errors are tied to the field by id). */
export function Field({
  id,
  label,
  error,
  hint,
  className,
  children,
}: {
  id: string
  label: React.ReactNode
  error?: string
  hint?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={cn('flex flex-col gap-2', className)}>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  )
}

export function FormError({ message }: { message?: string }) {
  return message ? (
    <p role="alert" className="text-sm text-destructive">
      {message}
    </p>
  ) : null
}

export function SubmitButton({
  busy,
  disabled,
  children,
  ...rest
}: { busy: boolean; disabled?: boolean; children: React.ReactNode } & Omit<
  React.ComponentProps<typeof Button>,
  'type' | 'disabled'
>) {
  return (
    <Button type="submit" disabled={busy || disabled} {...rest}>
      {busy ? <LoaderCircleIcon className="animate-spin" aria-hidden="true" /> : null}
      {children}
    </Button>
  )
}

/**
 * Maps an API failure onto a form: `errors[].field` paths become field errors (422), everything
 * else a form-level message. `aliases` maps server field names to form field names.
 */
export function useApplyFormError() {
  const { t, i18n } = useLingui()
  return <T extends FieldValues>(
    form: UseFormReturn<T>,
    error: unknown,
    aliases: Record<string, string> = {},
  ) => {
    if (error instanceof AppError) {
      if (error.isValidation && Object.keys(error.fieldErrors).length > 0) {
        for (const [field, issues] of Object.entries(error.fieldErrors)) {
          form.setError((aliases[field] ?? field) as Path<T>, {
            message: issues[0]?.message ?? t`This value is not valid`,
          })
        }
        return
      }
      if (error.isReauthRequired) return // the re-authentication dialog handles it
      if (error.isForbidden) {
        form.setError('root', { message: t`You do not have permission to do this.` })
        return
      }
      const known = problemMessage(error.code, i18n)
      if (known) {
        form.setError('root', { message: known })
        return
      }
    }
    form.setError('root', { message: t`Something went wrong. Please try again.` })
  }
}
