import { type FieldValues, type Path, type UseFormReturn } from 'react-hook-form'
import { useLingui } from '@lingui/react/macro'
import { LoaderCircleIcon } from 'lucide-react'
import { AppError } from '@/api/errors'
import { problemMessage } from '@/lib/labels'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'

/** Label + control + inline error, wired for screen readers through `aria-describedby`. */
export function FormField({
  id,
  label,
  error,
  hint,
  children,
}: {
  id: string
  label: React.ReactNode
  error?: string
  hint?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint ? (
        <p id={`${id}-hint`} className="text-xs text-muted-foreground">
          {hint}
        </p>
      ) : null}
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
  children,
  ...props
}: { busy: boolean } & React.ComponentProps<typeof Button>) {
  return (
    <Button type="submit" disabled={busy || props.disabled} {...props}>
      {busy ? <LoaderCircleIcon className="animate-spin" aria-hidden="true" /> : null}
      {children}
    </Button>
  )
}

/** Maps an API failure onto a react-hook-form instance: field errors where named, else a form message. */
export function useApplyApiError() {
  const { t, i18n } = useLingui()
  return <T extends FieldValues>(form: UseFormReturn<T>, error: unknown) => {
    if (error instanceof AppError) {
      if (error.isValidation && Object.keys(error.fieldErrors).length > 0) {
        for (const [field, issues] of Object.entries(error.fieldErrors)) {
          form.setError(field as Path<T>, {
            message: issues[0]?.message ?? t`This value is not valid`,
          })
        }
        return
      }
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
