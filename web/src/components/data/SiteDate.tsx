import { useLocale } from '@/app/locale'
import { formatSiteDate, type DateKind } from '@/lib/dates'

/** Renders a date or instant in the SITE's timezone, never the browser's (web plan §3). */
export function SiteDate({
  value,
  timeZone,
  kind = 'date',
  className,
}: {
  value: string | Date
  timeZone: string
  kind?: DateKind
  className?: string
}) {
  const { intl } = useLocale()
  const text = formatSiteDate(value, timeZone, intl, kind)
  if (!text) return null
  const iso = typeof value === 'string' ? value : value.toISOString()
  return (
    <time dateTime={iso} className={className}>
      {text}
    </time>
  )
}
