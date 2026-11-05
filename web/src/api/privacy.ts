import { useSyncExternalStore } from 'react'

/** IP addresses are masked in the panel by default; the choice is per browser (localStorage). */
const KEY = 'atoll.hideIPs'
const subs = new Set<() => void>()
const read = () => localStorage.getItem(KEY) !== '0'

export function setHideIPs(v: boolean) {
  localStorage.setItem(KEY, v ? '1' : '0')
  subs.forEach(f => f())
}

export function useHideIPs(): [boolean, (v: boolean) => void] {
  const v = useSyncExternalStore(f => {
    subs.add(f)
    const onStorage = (e: StorageEvent) => { if (e.key === KEY) f() }
    window.addEventListener('storage', onStorage)
    return () => { subs.delete(f); window.removeEventListener('storage', onStorage) }
  }, read, () => true)
  return [v, setHideIPs]
}

const V4 = /\b(\d{1,3})\.(\d{1,3})\.\d{1,3}\.\d{1,3}\b/g
// four or more colon-separated hex groups, or a "::" form (so times like 12:30:45 stay as they are)
const V6 = /\b(?:[0-9a-fA-F]{1,4}:){3,7}[0-9a-fA-F]{1,4}\b|\b(?:[0-9a-fA-F]{1,4}:)+:(?:[0-9a-fA-F]{1,4}:?)*/g

/** maskIPs keeps the first two parts of each address: 198.51.100.22 → 198.51.•.•, 2001:db8:1::5 → 2001:db8:•••. */
export function maskIPs(s: string): string {
  return s
    .replace(V4, (_m, a, b) => `${a}.${b}.•.•`)
    .replace(V6, m => {
      const g = m.split(':').filter(Boolean)
      return g.length >= 2 ? `${g[0]}:${g[1]}:•••` : m
    })
}

/** useMask returns maskIPs while IPs are hidden, identity otherwise: for IPs inside plain text. */
export function useMask(): (s: string) => string {
  const [hide] = useHideIPs()
  return hide ? maskIPs : (s: string) => s
}
