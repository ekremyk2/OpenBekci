import { useEffect, useId, useRef } from 'react'
import { Html5Qrcode } from 'html5-qrcode'

/**
 * Camera scanner (html5-qrcode). Loaded only through `QrScanner.tsx` (lazy) and only started when
 * the user asks for the camera.
 */
export default function QrScannerCore({
  onScan,
  onError,
}: {
  onScan: (text: string) => void
  onError: (message: string) => void
}) {
  const id = useId().replace(/:/g, '')
  const scanRef = useRef(onScan)
  const errRef = useRef(onError)
  useEffect(() => {
    scanRef.current = onScan
    errRef.current = onError
  })

  useEffect(() => {
    let cancelled = false
    const scanner = new Html5Qrcode(id)
    const stop = () =>
      scanner
        .stop()
        .then(() => scanner.clear())
        .catch(() => undefined)
    // `start()` and `stop()` are chained on one promise, so a quick unmount (or a StrictMode
    // double mount) can never stop a scanner that is still starting, nor start two on one element.
    const started = scanner
      .start(
        { facingMode: 'environment' },
        { fps: 10, qrbox: { width: 240, height: 240 } },
        (text) => {
          if (!cancelled) scanRef.current(text)
        },
        () => undefined,
      )
      .then(() => true)
      .catch((e: unknown) => {
        if (!cancelled) errRef.current(e instanceof Error ? e.message : String(e))
        return false
      })
    return () => {
      cancelled = true
      void started.then((ok) => (ok ? stop() : undefined))
    }
  }, [id])

  return <div id={id} className="w-full overflow-hidden rounded-md bg-black" />
}
