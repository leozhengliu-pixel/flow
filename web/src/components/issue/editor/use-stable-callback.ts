import { useCallback } from 'react'
import { useLatestRef } from '@/hooks/use-latest-ref'

/**
 * A function with a fixed identity that always runs the latest `callback` from the committed render. For getters
 * and handlers handed to editor extensions or other long-lived objects, which must not be rebuilt when props change.
 * Call it from handlers, effects or editor callbacks, never while rendering.
 */
export function useStableCallback<Args extends unknown[], Result>(callback: (...args: Args) => Result): (...args: Args) => Result {
  const latest = useLatestRef(callback)
  return useCallback((...args: Args) => latest.current(...args), [latest])
}
