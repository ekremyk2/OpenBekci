import { useLocale } from '@/app/locale'
import { currencySymbol, formatMoney, type Minor } from '@/lib/money'
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
  const text = formatMoney(minor, currency, intl, signDisplay ? { signDisplay } : {}).replace(
    '-',
    '\u2212',
  )
  // The symbol gets its own span so it can use Plex Sans; Plex Mono has no \u20ba.
  const symbol = currencySymbol(currency, intl)
  const at = text.indexOf(symbol)
  return (
    <span
      className={cn('money', negative && 'text-destructive', className)}
      dir="ltr" // digits and symbols keep their order inside RTL text
    >
      {at < 0 ? (
        text
      ) : (
        <>
          {text.slice(0, at)}
          <span className="money__symbol">{symbol}</span>
          {text.slice(at + symbol.length)}
        </>
      )}
    </span>
  )
}
