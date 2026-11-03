import { useEffect, useRef, useState } from 'react'
import { API, isDemo } from './client'
import { invalidate } from './hooks'
import type { BuildLogEvent, LiveEvent, LogLine } from './types'

export function wsUrl(path: string) {
  return `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}${API}${path}`
}

/** Reconnecting WebSocket. Returns a close function. */
export function openSocket(path: string, onMessage: (data: unknown) => void, onState?: (open: boolean) => void) {
  let ws: WebSocket | undefined, closed = false, tries = 0, timer: ReturnType<typeof setTimeout> | undefined
  const connect = () => {
    ws = new WebSocket(wsUrl(path))
    ws.onopen = () => { tries = 0; onState?.(true) }
    ws.onmessage = ev => { try { onMessage(JSON.parse(ev.data)) } catch { /* ignore non-JSON */ } }
    ws.onclose = () => {
      onState?.(false)
      if (closed) return
      timer = setTimeout(connect, Math.min(15000, 800 * 2 ** tries++))
    }
  }
  connect()
  return () => { closed = true; if (timer) clearTimeout(timer); ws?.close() }
}

const KEYS: Record<string, string[]> = {
  service: ['projects', 'project:', 'service:', 'domains', 'containers:', 'image-update'],
  server: ['servers', 'server:'],
  domain: ['domains', 'projects', 'project:', 'service:'],
  deployment: ['deployments:', 'service:', 'project:', 'projects', 'image-update'],
  confirmation: ['confirmations'],
  problems: ['problems'],
  image_update: ['image-update'],
  build_log: [], // high-volume: delivered to onBuildLog subscribers instead of invalidating queries
}

const buildLogSubs = new Set<(e: BuildLogEvent) => void>()
/** Subscribe to build_log frames of the shared /events socket. Returns an unsubscribe function. */
export function onBuildLog(fn: (e: BuildLogEvent) => void) { buildLogSubs.add(fn); return () => { buildLogSubs.delete(fn) } }

/** Live refresh: one /events socket; each frame invalidates the queries its type affects (batched). */
export function useLiveEvents(enabled: boolean) {
  useEffect(() => {
    if (!enabled || isDemo()) return
    let pending = new Set<string>(), t: ReturnType<typeof setTimeout> | undefined
    const close = openSocket('/events', d => {
      const ev = d as LiveEvent
      if (ev.type === 'build_log' && ev.payload) buildLogSubs.forEach(f => f(ev.payload as BuildLogEvent))
      const keys = KEYS[ev.type] ?? ['']
      if (!keys.length) return
      keys.forEach(k => pending.add(k))
      if (!t) t = setTimeout(() => { const p = [...pending]; pending = new Set(); t = undefined; invalidate(...p) }, 300)
    })
    return () => { close(); if (t) clearTimeout(t) }
  }, [enabled])
}

/** Streams LogLine frames from /services/{id}/logs. Keeps the last `max` lines. */
export function useLogStream(serviceId: string | undefined, tail = 200, max = 2000) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [live, setLive] = useState(false)
  const buf = useRef<LogLine[]>([])
  const [shownId, setShownId] = useState(serviceId)
  if (shownId !== serviceId) { setShownId(serviceId); setLines([]) } // new service: clear during render
  useEffect(() => {
    if (!serviceId) return
    buf.current = []
    let t: ReturnType<typeof setTimeout> | undefined
    const push = (l: LogLine) => {
      buf.current.push(l); if (buf.current.length > max) buf.current.splice(0, buf.current.length - max)
      if (!t) t = setTimeout(() => { t = undefined; setLines([...buf.current]) }, 120)
    }
    let close: () => void
    if (isDemo()) {
      let stop = false
      import('./demo').then(m => { if (!stop) close = m.demoLogStream(serviceId, push, setLive) })
      close = () => { stop = true }
      return () => { close(); if (t) clearTimeout(t) }
    }
    close = openSocket(`/services/${encodeURIComponent(serviceId)}/logs?tail=${tail}&follow=1`, d => push(d as LogLine), o => { if (o) buf.current = []; setLive(o) })
    return () => { close(); if (t) clearTimeout(t) }
  }, [serviceId, tail, max])
  return { lines, live, clear: () => { buf.current = []; setLines([]) } }
}
