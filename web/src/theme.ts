// Look of the panel in two parts:
//  - mode: light / dark, per device (localStorage atoll-theme, ?theme=)
//  - style: the preset theme (atoll, minimal, …), per account when the control server knows
//    PUT /me/theme, otherwise per device (localStorage atoll-style, ?style=)
// public/theme-boot.js applies both before the first paint; keep its lists in sync with this file.
import { useSyncExternalStore } from 'react'

export type Theme = 'dark' | 'light'
const KEY = 'atoll-theme'
export function initialTheme(): Theme {
  const q = new URLSearchParams(location.search).get('theme')
  if (q === 'light' || q === 'dark') return q
  const s = localStorage.getItem(KEY)
  if (s === 'light' || s === 'dark') return s
  return matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}
export function applyTheme(t: Theme) {
  const el = document.documentElement
  el.classList.remove('dark', 'light'); el.classList.add(t)
  localStorage.setItem(KEY, t)
  emit()
}
export function currentTheme(): Theme { return document.documentElement.classList.contains('light') ? 'light' : 'dark' }

export const STYLES = ['atoll', 'minimal', 'lagoon', 'macaron', 'contrast'] as const
export type Style = typeof STYLES[number]
export const DEFAULT_STYLE: Style = 'atoll'
const STYLE_KEY = 'atoll-style'
export const isStyle = (v: unknown): v is Style => typeof v === 'string' && (STYLES as readonly string[]).includes(v)
/** Ids a theme had before (saved on accounts and devices): zeabur became lagoon in v0.1.45;
 *  terminal was dropped in v0.1.46 and falls back to atoll. */
const RENAMED: Record<string, Style> = { zeabur: 'lagoon', terminal: 'atoll' }
export const toStyle = (v: unknown): Style | undefined => isStyle(v) ? v : typeof v === 'string' ? RENAMED[v] : undefined

/** ?style= in the address pins the style for this page load (screenshots, sharing a look). */
const urlStyle = (() => toStyle(new URLSearchParams(location.search).get('style')))()

export function initialStyle(): Style {
  if (urlStyle) return urlStyle
  return toStyle(localStorage.getItem(STYLE_KEY)) ?? DEFAULT_STYLE
}
export function currentStyle(): Style {
  const v = document.documentElement.dataset.theme
  return isStyle(v) ? v : DEFAULT_STYLE
}
export function applyStyle(s: Style, remember = true) {
  document.documentElement.dataset.theme = s
  if (remember) localStorage.setItem(STYLE_KEY, s)
  emit()
}
/** The account's saved theme from /auth/me. Empty (never chosen) or an id this panel does
 *  not know keeps the device's choice; ?style= wins for the page load. */
export function applyAccountStyle(id?: string) {
  const s = toStyle(id)
  if (urlStyle || !s || s === currentStyle()) return
  applyStyle(s)
}

const subs = new Set<() => void>()
function emit() { subs.forEach(f => f()) }
const subscribe = (f: () => void) => { subs.add(f); return () => { subs.delete(f) } }
/** Re-renders when the mode or the style changes. */
export function useLook(): { mode: Theme; style: Style } {
  const mode = useSyncExternalStore(subscribe, currentTheme)
  const style = useSyncExternalStore(subscribe, currentStyle)
  return { mode, style }
}
