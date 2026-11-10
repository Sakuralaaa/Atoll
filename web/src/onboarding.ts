// First-run wizard progress (pages/Onboarding.tsx). Browser-only: kept in localStorage per account
// and per control server (this panel's host + path), no backend fields.
//   status active  → the wizard opens on the projects page until it is finished or skipped
//   status done / skipped → never again, unless reopened from Settings (or ?onboarding=1)
export type OnboardingStatus = 'active' | 'done' | 'skipped'
export interface OnboardingState { status: OnboardingStatus; step: number; completed: number[]; updated_at: number }

export const ONBOARDING_STEPS = 5

const key = (user: string) => `atoll-onboarding:v1:${location.host}${location.pathname}:${user}`

export function readOnboarding(user: string | undefined): OnboardingState | undefined {
  if (!user) return undefined
  try {
    const v = JSON.parse(localStorage.getItem(key(user)) ?? 'null') as OnboardingState | null
    if (!v || !['active', 'done', 'skipped'].includes(v.status)) return undefined
    return { ...v, step: Math.min(Math.max(0, v.step | 0), ONBOARDING_STEPS - 1), completed: Array.isArray(v.completed) ? v.completed : [] }
  } catch { return undefined }
}

export function writeOnboarding(user: string, s: Omit<OnboardingState, 'updated_at'>) {
  try { localStorage.setItem(key(user), JSON.stringify({ ...s, updated_at: Date.now() })) } catch { /* private mode: progress is not kept */ }
}

/** Open the wizard from the first step (after the first admin is created, or reopened from Settings). */
export function startOnboarding(user: string) {
  writeOnboarding(user, { status: 'active', step: 0, completed: [] })
}

/** `?onboarding=1` in the page query or in the hash query (`#/?onboarding=1`). */
export function onboardingRequested(): boolean {
  if (new URLSearchParams(location.search).get('onboarding') === '1') return true
  const h = location.hash, i = h.indexOf('?')
  return i >= 0 && new URLSearchParams(h.slice(i + 1)).get('onboarding') === '1'
}
