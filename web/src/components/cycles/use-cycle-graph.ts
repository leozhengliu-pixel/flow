import { useEffect, useState } from 'react'

import { getCycleGraph, type CycleGraphData } from '@/lib/api'

/**
 * Loads the server-built cycle graph. `refreshKey` changes whenever the cycle's
 * issues change so the history is refetched; the previous graph stays visible
 * while a refresh is in flight.
 */
export function useCycleGraph(cycleId: string | undefined, refreshKey = '') {
  const [state, setState] = useState<{ cycleId?: string; graph?: CycleGraphData; error?: boolean }>({})
  useEffect(() => {
    if (!cycleId) return
    let cancelled = false
    const timer = window.setTimeout(() => {
      getCycleGraph(cycleId)
        .then(graph => { if (!cancelled) setState({ cycleId, graph }) })
        .catch(() => { if (!cancelled) setState(current => current.cycleId === cycleId ? { ...current, error: true } : { cycleId, error: true }) })
    }, refreshKey ? 250 : 0)
    return () => { cancelled = true; window.clearTimeout(timer) }
  }, [cycleId, refreshKey])
  const current = state.cycleId === cycleId ? state : {}
  return { graph: current.graph, error: Boolean(current.error), loading: !current.graph && !current.error }
}
