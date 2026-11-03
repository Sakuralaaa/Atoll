import { lazy, type ComponentType } from 'react'

/**
 * After the control server restarts on a new version, a tab that was opened before it still
 * runs the old entry chunk and asks for page chunks (assets/Servers-<oldhash>.js) that no longer
 * exist. The import fails and React used to unmount the whole app: a blank shell until a hard
 * reload. A chunk error now reloads the page once (to pick up the new index.html); if it happens
 * again within a minute the route error boundary shows a retry instead of looping.
 */
const KEY = 'atoll-chunk-reload-at'
const GUARD_MS = 60_000

export function isChunkLoadError(e: unknown): boolean {
  const m = e instanceof Error ? `${e.name} ${e.message}` : String(e)
  return /Failed to fetch dynamically imported module|error loading dynamically imported module|Importing a module script failed|Unable to preload CSS|ChunkLoadError|Loading (CSS )?chunk .* failed/i.test(m)
}

/** Reloads the page unless it already did so in the last minute; true when a reload was started. */
export function reloadOnce(): boolean {
  let last = 0
  try { last = Number(sessionStorage.getItem(KEY) || 0) } catch { /* storage blocked: still guard by not looping below */ }
  if (Date.now() - last < GUARD_MS) return false
  try { sessionStorage.setItem(KEY, String(Date.now())) } catch { return false }
  location.reload()
  return true
}

/** React.lazy that reloads once on a stale chunk instead of failing. */
export function lazyPage<T extends ComponentType<object>>(load: () => Promise<{ default: T }>) {
  return lazy(() => load().catch((e: unknown) => {
    if (isChunkLoadError(e) && reloadOnce()) return new Promise<{ default: T }>(() => {}) // the reload takes over
    throw e
  }))
}

/** Vite's own preload failures (CSS / modulepreload of a removed chunk) take the same path. */
export function installPreloadErrorReload() {
  window.addEventListener('vite:preloadError', (ev: Event) => {
    if (reloadOnce()) ev.preventDefault()
  })
}
