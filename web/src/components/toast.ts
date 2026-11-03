import { useSyncExternalStore } from 'react'

export type Tone = 'ok' | 'error' | 'info'
export type ToastAction = { label: string; run: () => void }
type T = { id: number; msg: string; tone: Tone; action?: ToastAction }
let toasts: T[] = []
let nextId = 1
const subs = new Set<() => void>()
const emit = () => subs.forEach(f => f())
export function toast(msg: string, tone: Tone = 'info', action?: ToastAction, ms?: number) {
  const id = nextId++
  toasts = [...toasts, { id, msg, tone, action }].slice(-4); emit()
  setTimeout(() => { toasts = toasts.filter(t => t.id !== id); emit() }, ms ?? (tone === 'error' ? 6000 : action ? 8000 : 3500))
  return id
}
export function dismissToast(id: number) { toasts = toasts.filter(t => t.id !== id); emit() }
export const useToasts = () => useSyncExternalStore(f => { subs.add(f); return () => { subs.delete(f) } }, () => toasts)
