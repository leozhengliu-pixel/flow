import { useEffect, useMemo, useState } from 'react'

/**
 * The current time as epoch milliseconds, safe to read while rendering (components must not call
 * `Date.now()` / `new Date()` in render). It is exact when the component mounts and refreshes every
 * `intervalMs` (default one minute); pass `0` to freeze it at mount. Call it once in a parent and pass the
 * value down to list rows instead of subscribing every row.
 */
export function useCurrentTimeMs(intervalMs = 60_000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (intervalMs <= 0) return
    const timer = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(timer)
  }, [intervalMs])
  return now
}

/** `useCurrentTimeMs` as a `Date` that is referentially stable until the clock refreshes. */
export function useCurrentTime(intervalMs = 60_000): Date {
  const now = useCurrentTimeMs(intervalMs)
  return useMemo(() => new Date(now), [now])
}
