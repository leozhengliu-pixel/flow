import { useState } from 'react'

const unset = Symbol('unset')

/**
 * Re-derive local state while rendering when `deps` change (React's "adjust state when a prop changes" pattern):
 * `reset` runs during the render that sees the new deps, so there is no extra commit with stale state and no effect.
 * By default it does not run on mount, so initialise the state from the same props; with `runOnMount` it also runs
 * during the first render (for state that cannot be initialised from the props). `reset` may only set state of the
 * calling component.
 */
export function useResetOnChange(deps: readonly unknown[], reset: () => void, { runOnMount = false }: { runOnMount?: boolean } = {}) {
  const [seen, setSeen] = useState<readonly unknown[] | typeof unset>(runOnMount ? unset : deps)
  if (seen === unset || seen.length !== deps.length || deps.some((dep, index) => !Object.is(dep, seen[index]))) {
    setSeen(deps)
    reset()
  }
}
