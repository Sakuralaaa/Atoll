export const OPEN_PALETTE = 'atoll:palette'
/** Opens the palette from anywhere (the sidebar search box). */
export const openPalette = () => dispatchEvent(new Event(OPEN_PALETTE))

/** Subsequence match; lower is better, -1 is no match. Contiguous and early hits score best. */
export function score(text: string, q: string): number {
  if (!q) return 0
  const t = text.toLowerCase(); const n = q.toLowerCase()
  const at = t.indexOf(n)
  if (at >= 0) return at
  let s = 100, i = 0, last = -1
  for (const ch of n) {
    const j = t.indexOf(ch, i)
    if (j < 0) return -1
    s += last < 0 ? j : j - last - 1
    last = j; i = j + 1
  }
  return s
}
