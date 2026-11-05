import { L } from '../i18n'
import { api } from './client'
import { useQuery } from './hooks'
import { toast } from '../components/toast'
import type { Project, ProjectPrefs } from './types'

// Project favorites (收藏) and pins (置顶) are kept per account on the server
// (GET/PUT /me/project-prefs), so they follow the user to every device.

const EMPTY: ProjectPrefs = { favorites: [], pinned: [] }

export function useProjectPrefs() {
  const q = useQuery('project-prefs', api.projectPrefs)
  const prefs = q.data ?? EMPTY
  /** Optimistic: show the change right away, roll back if the server refuses it. */
  const save = async (next: Partial<ProjectPrefs>) => {
    const before = prefs
    q.set({ ...prefs, ...next })
    try { q.set(await api.setProjectPrefs(next)) } catch (e) { q.set(before); toast((e as Error).message || L("保存失败"), 'error') }
  }
  const isFav = (id: string) => prefs.favorites.includes(id)
  const isPinned = (id: string) => prefs.pinned.includes(id)
  return {
    prefs, loaded: !!q.data, isFav, isPinned,
    toggleFav: (id: string) => save({ favorites: isFav(id) ? prefs.favorites.filter(x => x !== id) : [...prefs.favorites, id] }),
    togglePin: (id: string) => save({ pinned: isPinned(id) ? prefs.pinned.filter(x => x !== id) : [...prefs.pinned, id] }),
    /** save the 收藏 order after a drag (ids the caller did not show keep their place at the end) */
    reorderFavs: (ids: string[]) => save({ favorites: [...ids, ...prefs.favorites.filter(x => !ids.includes(x))] }),
    /** move a pinned project one place up (-1) or down (+1) */
    movePin: (id: string, d: -1 | 1) => {
      const ps = [...prefs.pinned], i = ps.indexOf(id), j = i + d
      if (i < 0 || j < 0 || j >= ps.length) return
      ;[ps[i], ps[j]] = [ps[j], ps[i]]
      save({ pinned: ps })
    },
    /** drop `id` before `before` (drag and drop between pinned cards) */
    dropPin: (id: string, before: string) => {
      if (id === before) return
      const ps = prefs.pinned.filter(x => x !== id), j = ps.indexOf(before)
      if (j < 0) return
      ps.splice(j, 0, id)
      save({ pinned: ps })
    },
  }
}

/** Pinned projects first, in their pinned order; the rest keep the order they came in. */
export function pinnedFirst<P extends Pick<Project, 'id'>>(list: P[], pinned: string[]): P[] {
  const rank = new Map(pinned.map((id, i) => [id, i]))
  return list.map((p, i) => [p, i] as const)
    .sort(([a, i], [b, j]) => (rank.get(a.id) ?? 1e9 + i) - (rank.get(b.id) ?? 1e9 + j))
    .map(([p]) => p)
}
