import { useSyncExternalStore } from 'react'
import en from './en'

/** Minimal zh/en switch. Strings stay next to the UI that uses them: t('中文', 'English'). */
export type Lang = 'zh' | 'en'
const KEY = 'atoll-lang'
const subs = new Set<() => void>()

export function getLang(): Lang {
  const q = new URLSearchParams(location.search).get('lang')
  if (q === 'zh' || q === 'en') return q
  const v = localStorage.getItem(KEY)
  if (v === 'zh' || v === 'en') return v
  // Chinese unless the user picked English: most screens are still Chinese-only, so following an
  // English browser locale gave a Chinese page with English islands (e.g. the agent-upgrade banner).
  return 'zh'
}
export function setLang(l: Lang) {
  if (l === getLang()) return
  localStorage.setItem(KEY, l)
  document.documentElement.lang = l === 'zh' ? 'zh-CN' : 'en'
  subs.forEach(f => f())
  // Many labels are computed once at module load (L() in constants), so reload to apply everywhere.
  const u = new URL(location.href)
  if (u.searchParams.has('lang')) { u.searchParams.delete('lang'); location.replace(u.toString()) } else location.reload()
}

const current: Lang = getLang()
document.documentElement.lang = current === 'zh' ? 'zh-CN' : 'en'
/** Translate Chinese source text: L('{0} 个服务', n). Returns the Chinese when the language is zh or no
 *  English exists. The language is fixed per page load; setLang reloads. */
export function L(zh: string, ...args: unknown[]): string {
  const s = current === 'en' && zh in en ? en[zh] : zh
  return args.length ? s.replace(/\{(\d+)\}/g, (m, i) => (+i < args.length ? String(args[+i]) : m)) : s
}
const subscribe = (f: () => void) => { subs.add(f); return () => { subs.delete(f) } }

/** Re-renders on language change; returns the current language and a translator. */
export function useT() {
  const lang = useSyncExternalStore(subscribe, getLang)
  const t = (zh: string, en: string) => (lang === 'en' ? en : zh)
  return { lang, t }
}
