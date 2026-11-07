import { useEffect, useState } from 'react'
import { Controller, type Control, type FieldPath, type FieldValues } from 'react-hook-form'
import { useLingui } from '@lingui/react/macro'
import { z } from 'zod'
import { MoneyInput } from '@/components/forms/MoneyInput'
import { Field } from './ui'

/** Debounces a fast-changing value (live previews should not fire on every keystroke). */
export function useDebounced<T>(value: T, ms = 400): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return v
}

/** Amount held as bigint minor units in the form state; null while empty or unreadable. */
export function MoneyFormField<T extends FieldValues>({
  control,
  name,
  label,
  currency,
  error,
  hint,
  id,
}: {
  control: Control<T>
  name: FieldPath<T>
  label: React.ReactNode
  currency: string
  error?: string
  hint?: React.ReactNode
  id: string
}) {
  return (
    <Field label={label} htmlFor={id} error={error} hint={hint}>
      <Controller
        control={control}
        name={name}
        render={({ field }) => (
          <MoneyInput
            id={id}
            currency={currency}
            value={(field.value as bigint | null) ?? null}
            onChange={field.onChange}
            onBlur={field.onBlur}
            aria-invalid={error ? true : undefined}
          />
        )}
      />
    </Field>
  )
}

/** zod building blocks; messages are translated, so schemas are built inside components. */
export function useZodMessages() {
  const { t } = useLingui()
  return {
    required: t`This field is required.`,
    amount: t`Enter an amount greater than zero.`,
    date: t`Enter a date.`,
    pick: t`Choose one.`,
  }
}

export const positiveMinor = (message: string) =>
  z.custom<bigint>((v) => typeof v === 'bigint' && v > 0n, { message })

export const nonNegativeMinor = (message: string) =>
  z.custom<bigint>((v) => typeof v === 'bigint' && v >= 0n, { message })

export const isoDate = (message: string) => z.string().regex(/^\d{4}-\d{2}-\d{2}$/, message)

export const minorToMoney = (v: bigint, currency: string) => ({ amount: Number(v), currency })
