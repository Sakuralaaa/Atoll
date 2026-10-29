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
}
export function currentTheme(): Theme { return document.documentElement.classList.contains('light') ? 'light' : 'dark' }
