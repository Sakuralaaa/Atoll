import { useEffect, useState } from 'react'

/** Wall-clock time as state, re-read every `everyMs`, so render stays pure (no Date.now() in render). */
export function useNow(everyMs = 30000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), everyMs); return () => clearInterval(t) }, [everyMs])
  return now
}
