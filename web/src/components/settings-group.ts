import { useLocation } from 'react-router-dom'

export type SettingsGroup = { k: string; l: string; icon: React.ReactNode }

// The settings pages show one group at a time instead of one long scroll.
// The group lives in the hash (#/settings#backup) so problem links keep working.
export function useSettingsGroup(groups: SettingsGroup[]) {
  const loc = useLocation()
  const k = loc.hash.slice(1)
  return groups.some(g => g.k === k) ? k : groups[0]?.k
}
