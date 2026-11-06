import { useEffect, useRef, useState } from 'react'
import { useLocale } from '@/app/locale'
import { Input } from '@/components/ui/input'
import { formatMoneyPlain, parseMoneyInput } from '@/lib/money'

/**
 * Amount field working in integer minor units. The text is parsed with the user's locale on every
 * keystroke; `onChange` receives minor units (or null while empty/invalid) and the text is
 * normalised on blur. Shows the currency code as an adornment.
 */
export function MoneyInput({
  value,
  onChange,
  currency,
  id,
  ...rest
}: {
  value: bigint | null
  onChange: (minor: bigint | null) => void
  currency: string
} & Omit<React.ComponentProps<typeof Input>, 'value' | 'onChange' | 'type'>) {
  const { intl } = useLocale()
  const [text, setText] = useState(() =>
    value === null ? '' : formatMoneyPlain(value, currency, intl),
  )
  const lastEmitted = useRef<bigint | null>(value)

  // Re-sync when the controlled value changes from outside (form reset, locale switch).
  useEffect(() => {
    if (value !== lastEmitted.current) {
      lastEmitted.current = value
      setText(value === null ? '' : formatMoneyPlain(value, currency, intl))
    }
  }, [value, currency, intl])

  // A locale or currency switch changes the separators: re-render the parsed amount in the new
  // format (text that does not parse yet is left for the user to finish).
  useEffect(() => {
    const minor = lastEmitted.current
    if (minor !== null) setText(formatMoneyPlain(minor, currency, intl))
  }, [currency, intl])

  const invalid = text.trim() !== '' && parseMoneyInput(text, currency, intl) === null

  return (
    <div className="relative">
      <Input
        {...rest}
        id={id}
        type="text"
        inputMode="decimal"
        autoComplete="off"
        dir="ltr"
        className="pe-14 text-end tabular-nums"
        aria-invalid={invalid || rest['aria-invalid'] ? true : undefined}
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          const minor = parseMoneyInput(e.target.value, currency, intl)
          lastEmitted.current = minor
          onChange(minor)
        }}
        onBlur={(e) => {
          const minor = parseMoneyInput(text, currency, intl)
          if (minor !== null) setText(formatMoneyPlain(minor, currency, intl))
          rest.onBlur?.(e)
        }}
      />
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-y-0 end-3 flex items-center text-sm text-muted-foreground"
      >
        {currency}
      </span>
    </div>
  )
}
