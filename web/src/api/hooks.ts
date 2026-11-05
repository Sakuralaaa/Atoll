import { L } from '../i18n'
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ApiError, isConfirmation } from './client'
import { toast } from '../components/toast'

// ---- tiny query cache: key -> last data, plus invalidation by key prefix ----
const cache = new Map<string, unknown>()
const listeners = new Set<(key: string) => void>()

/** Refetch every mounted query whose key starts with one of the prefixes (no args = all). */
export function invalidate(...prefixes: string[]) {
  for (const k of [...cache.keys()]) if (!prefixes.length || prefixes.some(p => k.startsWith(p))) cache.delete(k)
  listeners.forEach(l => prefixes.length ? prefixes.forEach(p => l(p)) : l(''))
}

export interface Query<T> { data: T | undefined; loading: boolean; error: ApiError | Error | undefined; refetch: () => void; set: (v: T) => void }

/** useQuery-like hook. `key` null = disabled. Data is cached per key so revisits render instantly. */
export function useQuery<T>(key: string | null, fn: () => Promise<T>, opts: { interval?: number } = {}): Query<T> {
  const [data, setData] = useState<T | undefined>(() => (key ? cache.get(key) as T | undefined : undefined))
  const [loading, setLoading] = useState(key !== null && !cache.has(key))
  const [error, setError] = useState<Error | undefined>()
  const fnRef = useRef(fn)
  useLayoutEffect(() => { fnRef.current = fn })
  const seq = useRef(0)

  const load = useCallback((quiet = false) => {
    if (key === null) return
    const my = ++seq.current
    if (!quiet) setLoading(!cache.has(key))
    fnRef.current().then(d => {
      if (my !== seq.current) return
      cache.set(key, d); setData(d); setError(undefined); setLoading(false)
    }, (e: Error) => {
      if (my !== seq.current) return
      setError(e); setLoading(false)
    })
  }, [key])

  // key changed: show the cached value (or nothing) for the new key right away, during render
  const [shownKey, setShownKey] = useState(key)
  if (shownKey !== key) {
    setShownKey(key)
    setData(key === null ? undefined : cache.get(key) as T | undefined)
    setLoading(key !== null && !cache.has(key))
  }

  useEffect(() => {
    if (key === null) return
    // data fetch: loading was set for this key during render; state changes only when the request settles
    // oxlint-disable-next-line react/set-state-in-effect
    load(true)
    const l = (p: string) => { if (key.startsWith(p)) load(true) }
    listeners.add(l)
    const t = opts.interval ? setInterval(() => load(true), opts.interval) : undefined
    return () => { listeners.delete(l); if (t) clearInterval(t) }
  }, [key, load, opts.interval])

  const set = useCallback((v: T) => { if (key) cache.set(key, v); setData(v) }, [key])
  return { data, loading, error, refetch: () => load(true), set }
}

/** Run a mutation: toast errors, optionally toast success, invalidate keys. Returns the result or undefined on failure. */
export async function run<T>(fn: () => Promise<T>, o: { ok?: string; invalidate?: string[] } = {}): Promise<T | undefined> {
  try {
    const r = await fn()
    if (isConfirmation(r)) toast(L("已提交，等待在「审批」中批准"), 'info')
    else if (o.ok) toast(o.ok, 'ok')
    if (o.invalidate) invalidate(...o.invalidate)
    return r
  } catch (e) {
    toast((e as Error).message || L("操作失败"), 'error')
    return undefined
  }
}

/** For button-driven mutations: [busy, wrap]. */
export function useBusy() {
  const [busy, setBusy] = useState(false)
  const wrap = useCallback(async <T,>(fn: () => Promise<T>) => { setBusy(true); try { return await fn() } finally { setBusy(false) } }, [])
  return [busy, wrap] as const
}
