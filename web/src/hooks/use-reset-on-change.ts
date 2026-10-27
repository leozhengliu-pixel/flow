import { useState } from 'react'

/**
 * Re-derive local state while rendering when `deps` change (React's "adjust state when a prop changes" pattern):
 * `reset` runs during the render that sees the new deps, so there is no extra commit with stale state and no effect.
 * It does not run on mount, so initialise the state from the same props. `reset` may only set state of the calling
 * component.
 */
export function useResetOnChange(deps: readonly unknown[], reset: () => void) {
  const [seen, setSeen] = useState(deps)
  if (seen.length !== deps.length || deps.some((dep, index) => !Object.is(dep, seen[index]))) {
    setSeen(deps)
    reset()
  }
}
