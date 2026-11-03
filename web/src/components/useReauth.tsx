import { useCallback, useState } from 'react'
import { ReauthDialog } from './Reauth'
import { toast } from './toast'
import { setUntil, reauthValid } from './reauthWindow'
import type { ApiError } from '../api/client'

type Pending = { title?: string; desc?: React.ReactNode; resolve: (ok: boolean) => void }

/** `ensure()` asks for the password unless the window is open; `guard(fn)` runs fn inside a valid
 *  window, asks again once when the server answers reauth_required, and toasts other errors
 *  (or rethrows them with `rethrow`). Render `dialog` somewhere in the component. */
export function useReauth() {
  const [pending, setPending] = useState<Pending>()
  const ensure = useCallback((title?: string, desc?: React.ReactNode) =>
    reauthValid() ? Promise.resolve(true) : new Promise<boolean>(resolve => setPending({ title, desc, resolve })), [])
  const guard = useCallback(async <T,>(fn: () => Promise<T>, o: { title?: string; desc?: React.ReactNode; rethrow?: boolean } = {}): Promise<T | undefined> => {
    for (let attempt = 0; attempt < 2; attempt++) {
      if (!await ensure(o.title, o.desc)) return undefined
      try { return await fn() } catch (x) {
        const err = x as ApiError
        if (err.status === 401 && err.code === 'reauth_required' && attempt === 0) { setUntil(0); continue }
        if (o.rethrow) throw err
        toast(err.message || '操作失败', 'error'); return undefined
      }
    }
    return undefined
  }, [ensure])
  const dialog = pending ? <ReauthDialog title={pending.title} desc={pending.desc} onDone={ok => { pending.resolve(ok); setPending(undefined) }} /> : null
  return { ensure, guard, dialog }
}
