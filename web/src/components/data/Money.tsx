import { useLocale } from '@/app/locale'
import { formatMoney, type Minor } from '@/lib/money'
import { cn } from '@/lib/utils'

/** The only way to show an amount (web plan §10). Minor units in, locale-formatted text out. */
export function Money({
  minor,
  currency,
  signDisplay,
  className,
}: {
  minor: Minor
  currency: string
  signDisplay?: Intl.NumberFormatOptions['signDisplay']
  className?: string
}) {
  const { intl } = useLocale()
  const negative = typeof minor === 'string' ? minor.startsWith('-') : minor < 0
  return (
    <span
      className={cn('tabular-nums', negative && 'text-destructive', className)}
      dir="ltr" // digits and symbols keep their order inside RTL text
    >
      {formatMoney(minor, currency, intl, signDisplay ? { signDisplay } : {})}
    </span>
  )
}
