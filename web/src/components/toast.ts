import { useSyncExternalStore } from 'react'

export type Tone = 'ok' | 'error' | 'info'
type T = { id: number; msg: string; tone: Tone }
let toasts: T[] = []
let nextId = 1
const subs = new Set<() => void>()
const emit = () => subs.forEach(f => f())
export function toast(msg: string, tone: Tone = 'info') {
  const id = nextId++
  toasts = [...toasts, { id, msg, tone }].slice(-4); emit()
  setTimeout(() => { toasts = toasts.filter(t => t.id !== id); emit() }, tone === 'error' ? 6000 : 3500)
}
export function dismissToast(id: number) { toasts = toasts.filter(t => t.id !== id); emit() }
export const useToasts = () => useSyncExternalStore(f => { subs.add(f); return () => { subs.delete(f) } }, () => toasts)
