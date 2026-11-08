import { useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { problemMessage } from '@/lib/labels'

/** A user-facing sentence for any failure; used where there is no react-hook-form instance. */
export function useApplyErrorMessage() {
  const { t, i18n } = useLingui()
  return (error: unknown): string => {
    if (error instanceof AppError) {
      if (error.isForbidden) return t`You do not have permission to do this.`
      const first = Object.values(error.fieldErrors)[0]?.[0]?.message
      if (first) return first
      const known = problemMessage(error.code, i18n)
      if (known) return known
    }
    return t`Something went wrong. Please try again.`
  }
}
