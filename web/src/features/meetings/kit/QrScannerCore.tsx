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
  scanRef.current = onScan
  errRef.current = onError

  useEffect(() => {
    let cancelled = false
    const scanner = new Html5Qrcode(id)
    let started = false
    scanner
      .start(
        { facingMode: 'environment' },
        { fps: 10, qrbox: { width: 240, height: 240 } },
        (text) => scanRef.current(text),
        () => undefined,
      )
      .then(() => {
        started = true
        if (cancelled) void scanner.stop().then(() => scanner.clear())
      })
      .catch((e: unknown) => errRef.current(e instanceof Error ? e.message : String(e)))
    return () => {
      cancelled = true
      if (started) void scanner.stop().then(() => scanner.clear())
    }
  }, [id])

  return <div id={id} className="w-full overflow-hidden rounded-md bg-black" />
}
