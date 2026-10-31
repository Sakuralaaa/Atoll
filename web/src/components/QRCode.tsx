import { useMemo } from 'react'
import { encode } from 'uqr'

/** QR code rendered locally as SVG (dark modules on white, so phone cameras read it in either theme). */
export default function QRCode({ value, size = 196, label }: { value: string; size?: number; label?: string }) {
  const { path, n } = useMemo(() => {
    const qr = encode(value, { ecc: 'M', border: 2 })
    let d = ''
    qr.data.forEach((row, y) => row.forEach((on, x) => { if (on) d += `M${x} ${y}h1v1h-1z` }))
    return { path: d, n: qr.size }
  }, [value])
  return (
    <svg role="img" aria-label={label ?? 'QR code'} width={size} height={size} viewBox={`0 0 ${n} ${n}`} shapeRendering="crispEdges"
      className="block rounded-[4px] border-2 border-[var(--edge)] bg-white">
      <rect width={n} height={n} fill="#ffffff" />
      <path d={path} fill="#141414" />
    </svg>
  )
}
