import { useSyncExternalStore } from 'react'

/** Minimal zh/en switch. Strings stay next to the UI that uses them: t('中文', 'English'). */
export type Lang = 'zh' | 'en'
const KEY = 'atoll-lang'
const subs = new Set<() => void>()

export function getLang(): Lang {
  const v = localStorage.getItem(KEY)
  if (v === 'zh' || v === 'en') return v
  // Chinese unless the user picked English: most screens are still Chinese-only, so following an
  // English browser locale gave a Chinese page with English islands (e.g. the agent-upgrade banner).
  return 'zh'
}
export function setLang(l: Lang) {
  localStorage.setItem(KEY, l)
  document.documentElement.lang = l === 'zh' ? 'zh-CN' : 'en'
  subs.forEach(f => f())
}
const subscribe = (f: () => void) => { subs.add(f); return () => { subs.delete(f) } }

/** Re-renders on language change; returns the current language and a translator. */
export function useT() {
  const lang = useSyncExternalStore(subscribe, getLang)
  const t = (zh: string, en: string) => (lang === 'en' ? en : zh)
  return { lang, t }
}
