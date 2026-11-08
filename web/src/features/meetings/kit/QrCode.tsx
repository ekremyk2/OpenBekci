import { lazy, Suspense } from 'react'
import { Skeleton } from '@/components/ui/skeleton'

// `qrcode` is only needed where a QR is drawn: keep it out of the initial route chunks.
const QrSvg = lazy(() => import('./QrSvg'))

/** Draws `value` as a QR code (lazy-loaded). */
export function QrCode({
  value,
  size = 224,
  label,
}: {
  value: string
  size?: number
  label: string
}) {
  return (
    <Suspense fallback={<Skeleton className="size-56 rounded-md" />}>
      <QrSvg value={value} size={size} label={label} />
    </Suspense>
  )
}
