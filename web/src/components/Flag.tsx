import { useEffect, useState } from 'react'

// One lazy chunk per country (about 1 KB each), loaded only for flags on screen. SVG rather
// than the regional-indicator emoji, which Windows shows as two plain letters.
const FLAGS = import.meta.glob<string>('../../node_modules/country-flag-icons/string/3x2/[A-Z][A-Z].js', { import: 'default' })
const cache = new Map<string, string>()

function loader(code: string) {
  return FLAGS[`../../node_modules/country-flag-icons/string/3x2/${code}.js`]
}

/** A country flag for an ISO 3166-1 alpha-2 code; nothing for an unknown code. */
export function Flag({ code, size = 14, className }: { code?: string; size?: number; className?: string }) {
  const cc = (code ?? '').toUpperCase()
  const [, setLoaded] = useState(0)
  useEffect(() => {
    if (cache.has(cc)) return
    const load = /^[A-Z]{2}$/.test(cc) ? loader(cc) : undefined
    if (!load) return
    let live = true
    load().then(v => { cache.set(cc, v); if (live) setLoaded(n => n + 1) }).catch(() => {})
    return () => { live = false }
  }, [cc])
  const svg = cache.get(cc)
  if (!svg) return null
  return <img src={`data:image/svg+xml;utf8,${encodeURIComponent(svg)}`} alt={cc} title={cc} width={Math.round(size * 1.5)} height={size}
    className={`inline-block shrink-0 rounded-[2px] ring-1 ring-black/10 object-cover ${className ?? ''}`} />
}
