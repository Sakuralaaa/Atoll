// ---- the window (module-wide: one per tab, like the server's per-session window) ----
let until = 0
export const reauthUntil = () => until
export const reauthSubs = new Set<() => void>()
export const setUntil = (t: number) => { until = t; reauthSubs.forEach(f => f()) }
/** 15 s margin so an action never races the server-side expiry. */
export const reauthValid = () => Date.now() < until - 15000

