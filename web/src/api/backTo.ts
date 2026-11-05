import { useEffect, useRef } from 'react'
import { useLocation, useNavigationType } from 'react-router-dom'

// Detail pages (a project with its services, a server) remember the page they were opened
// from, so their back link returns there: 我的项目 → server → project → back lands on the
// server, and back again on 我的项目. Kept per tab (sessionStorage), keyed by the detail scope.

const KEY = 'atoll.backTo.'
const SCROLL = 'atoll.scroll.'
/** Router state for a back link, so the page it returns to restores its scroll position. */
export const BACK = { back: true }
const scopeOf = (path: string) => path.match(/^\/(projects|servers)\/[^/]+|^\/import\/docker$|^\/settings\/security$/)?.[0] ?? ''

/** Mount once (App): records where each detail scope was entered from. */
export function useTrackBackTo() {
  const loc = useLocation()
  const navType = useNavigationType()
  const prev = useRef<string | null>(null)
  const key = useRef('')
  useEffect(() => { key.current = loc.pathname + loc.search }, [loc.pathname, loc.search])
  // remember how far each page's main area was scrolled
  useEffect(() => {
    const onScroll = (e: Event) => {
      const el = e.target
      if (!(el instanceof HTMLElement) || el.tagName !== 'MAIN') return
      try { sessionStorage.setItem(SCROLL + key.current, String(Math.round(el.scrollTop))) } catch { /* ignore */ }
    }
    document.addEventListener('scroll', onScroll, true)
    return () => document.removeEventListener('scroll', onScroll, true)
  }, [])
  // …and put it back when returning through a back link or the browser's back button
  useEffect(() => {
    const back = navType === 'POP' || (loc.state as { back?: boolean } | null)?.back
    if (!back) return
    let want = 0
    try { want = Number(sessionStorage.getItem(SCROLL + loc.pathname + loc.search)) || 0 } catch { /* ignore */ }
    if (!want) return
    let tries = 0
    const id = window.setInterval(() => {
      const main = document.querySelector('main')
      // content arrives with its query; wait until the page is tall enough (up to ~1.5s)
      if (main && (main.scrollHeight - main.clientHeight >= want || tries > 15)) { main.scrollTop = want; window.clearInterval(id) }
      if (++tries > 16) window.clearInterval(id)
    }, 100)
    return () => window.clearInterval(id)
  }, [loc.key]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    const here = loc.pathname + loc.search
    const from = prev.current
    prev.current = here
    if (from === null) return
    const s = scopeOf(loc.pathname)
    if (!s || scopeOf(from.split('?')[0]) === s) return
    if (from === '/login' || from.startsWith('/login?')) return
    try {
      // coming back (server → project → back to the server) keeps the server's own origin
      const fromScope = scopeOf(from.split('?')[0])
      const theirs = fromScope && sessionStorage.getItem(KEY + fromScope)
      if (theirs && scopeOf(theirs.split('?')[0]) === s) return
      sessionStorage.setItem(KEY + s, from)
    } catch { /* private mode */ }
  }, [loc.pathname, loc.search])
}

/** Where the back link of the detail scope `scope` (`/projects/x`, `/servers/y`) goes. */
export function backTo(scope: string, fallback: string): string {
  try { return sessionStorage.getItem(KEY + scope) || fallback } catch { return fallback }
}

/** Short label for a back link to `path`. */
export function backLabel(path: string, names: { server?: (id: string) => string | undefined; project?: (id: string) => string | undefined } = {}): string {
  const [p, q = ''] = path.split('?')
  const grouped = p === '/' && new URLSearchParams(q).get('server') // 我的项目 narrowed to one machine
  if (grouped) return (grouped !== '_' && names.server?.(grouped)) || '项目'
  const m = p.match(/^\/(projects|servers)\/([^/]+)/)
  if (m) return (m[1] === 'servers' ? names.server?.(m[2]) : names.project?.(m[2])) ?? (m[1] === 'servers' ? '服务器' : '项目')
  const top: Record<string, string> = { '/': '项目', '/servers': '服务器', '/control': '中控', '/domains': '域名', '/developer': '连接', '/confirmations': '审批', '/import': '迁移', '/settings': '设置' }
  return top[p] ?? (p.startsWith('/settings') ? '设置' : p.startsWith('/import') ? '迁移' : '返回')
}
