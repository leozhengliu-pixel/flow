import { useState } from 'react'

/**
 * Like `useMemo`, but `compute` may read values that are deliberately not dependencies (the latest bootstrap data,
 * a clock): it re-runs only when one of `deps` changes, and keeps its result across every other render.
 */
export function useDepsSnapshot<T>(deps: readonly unknown[], compute: () => T): T {
  const [snapshot, setSnapshot] = useState(() => ({ deps, value: compute() }))
  if (snapshot.deps.length === deps.length && snapshot.deps.every((dep, index) => Object.is(dep, deps[index]))) return snapshot.value
  const value = compute()
  setSnapshot({ deps, value })
  return value
}
