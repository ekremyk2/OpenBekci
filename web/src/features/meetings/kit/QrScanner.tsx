import { lazy, Suspense, useRef, useState } from 'react'
import { CameraIcon, CameraOffIcon } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'

const QrScannerCore = lazy(() => import('./QrScannerCore'))

/**
 * Scan a QR with the device camera (lazy-loaded; the camera starts only on request) or type or
 * paste the code (a USB scanner types into the field like a keyboard). Both paths call `onScan`.
 */
export function QrScanner({
  onScan,
  busy,
  large,
  scanLabel,
}: {
  onScan: (text: string) => void
  busy?: boolean
  large?: boolean
  scanLabel?: string
}) {
  const { t } = useLingui()
  const [camera, setCamera] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [code, setCode] = useState('')
  const last = useRef<{ text: string; at: number } | null>(null)

  function emit(text: string) {
    const v = text.trim()
    if (!v) return
    const now = Date.now()
    // the camera reports the same frame many times a second
    if (last.current && last.current.text === v && now - last.current.at < 4000) return
    last.current = { text: v, at: now }
    onScan(v)
  }

  return (
    <div className="flex flex-col gap-3">
      {camera ? (
        <div className="flex flex-col gap-2">
          <Suspense fallback={<Skeleton className="h-64 w-full" />}>
            <QrScannerCore
              onScan={emit}
              onError={(m) => {
                setCamera(false)
                setError(m)
              }}
            />
          </Suspense>
        </div>
      ) : null}
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {t`The camera is not available (${error}). Type the code instead.`}
        </p>
      ) : null}
      <Button
        type="button"
        variant={camera ? 'outline' : 'default'}
        size={large ? 'lg' : 'default'}
        onClick={() => {
          setError(null)
          setCamera((c) => !c)
        }}
      >
        {camera ? <CameraOffIcon /> : <CameraIcon />}
        {camera ? t`Stop camera` : (scanLabel ?? t`Scan with camera`)}
      </Button>
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          last.current = null
          emit(code)
          setCode('')
        }}
      >
        <Input
          aria-label={t`QR code`}
          placeholder={t`or type the code`}
          dir="ltr"
          autoComplete="off"
          value={code}
          onChange={(e) => setCode(e.target.value)}
          className={large ? 'h-12 text-lg' : undefined}
        />
        <Button type="submit" variant="secondary" size={large ? 'lg' : 'default'} disabled={busy || code.trim() === ''}>
          {t`Check`}
        </Button>
      </form>
    </div>
  )
}
