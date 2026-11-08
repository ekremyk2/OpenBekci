import { useMemo } from 'react'
import QRCode from 'qrcode'

/**
 * The QR as an inline <svg> built from the module matrix: no data: URLs and no
 * dangerouslySetInnerHTML, so a strict CSP holds. This file imports the `qrcode` library and is
 * only ever loaded through `QrCode.tsx` (lazy).
 */
export default function QrSvg({
  value,
  size = 224,
  label,
}: {
  value: string
  size?: number
  label: string
}) {
  const { n, d } = useMemo(() => {
    const qr = QRCode.create(value, { errorCorrectionLevel: 'M' })
    const n = qr.modules.size
    const data = qr.modules.data
    let d = ''
    for (let y = 0; y < n; y++) {
      let x = 0
      while (x < n) {
        if (data[y * n + x]) {
          let run = 1
          while (x + run < n && data[y * n + x + run]) run++
          d += `M${x} ${y}h${run}v1h-${run}z`
          x += run
        } else x++
      }
    }
    return { n, d }
  }, [value])
  const quiet = 2
  return (
    <svg
      role="img"
      aria-label={label}
      width={size}
      height={size}
      viewBox={`${-quiet} ${-quiet} ${n + quiet * 2} ${n + quiet * 2}`}
      shapeRendering="crispEdges"
      className="rounded-md bg-white"
    >
      <rect x={-quiet} y={-quiet} width={n + quiet * 2} height={n + quiet * 2} fill="#fff" />
      <path d={d} fill="#000" />
    </svg>
  )
}
