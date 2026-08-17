import type { IssueUpdateInput } from '@/types/flow'
import { useCallback, useEffect, useRef, useState } from 'react'
import type { IssueSaveState } from '../issue-header'

export function useIssueAutosave(onSave: (input: IssueUpdateInput) => Promise<void>, delay = 600) {
  const [state, setState] = useState<IssueSaveState>('idle')
  const saveRef = useRef(onSave)
  const pendingRef = useRef<IssueUpdateInput | null>(null)
  const failedRef = useRef<IssueUpdateInput | null>(null)
  const timerRef = useRef<number | undefined>(undefined)
  const drainRef = useRef<Promise<boolean> | null>(null)
  const mountedRef = useRef(true)
  const idleTimerRef = useRef<number | undefined>(undefined)
  saveRef.current = onSave

  const flush = useCallback((): Promise<boolean> => {
    window.clearTimeout(timerRef.current)
    if (drainRef.current) return drainRef.current
    if (!pendingRef.current) return Promise.resolve(!failedRef.current)

    const drain = (async () => {
      while (pendingRef.current) {
        const input = pendingRef.current
        pendingRef.current = null
        if (mountedRef.current) setState('saving')
        try {
          await saveRef.current(input)
          failedRef.current = null
        } catch {
          // Merge edits made while this request was in flight over the failed snapshot.
          const newer = pendingRef.current as IssueUpdateInput | null
          pendingRef.current = newer ? { ...input, ...newer } : input
          failedRef.current = pendingRef.current
          if (mountedRef.current) setState('error')
          return false
        }
      }

      if (mountedRef.current) {
        setState('saved')
        window.clearTimeout(idleTimerRef.current)
        idleTimerRef.current = window.setTimeout(() => setState('idle'), 900)
      }
      return true
    })().finally(() => {
      drainRef.current = null
    })
    drainRef.current = drain
    return drain
  }, [])

  const schedule = useCallback((input: IssueUpdateInput) => {
    pendingRef.current = { ...pendingRef.current, ...input }
    failedRef.current = null
    window.clearTimeout(timerRef.current)
    window.clearTimeout(idleTimerRef.current)
    setState('saving')
    timerRef.current = window.setTimeout(() => void flush(), delay)
  }, [delay, flush])

  const retry = useCallback(() => {
    if (failedRef.current) pendingRef.current = { ...failedRef.current, ...pendingRef.current }
    failedRef.current = null
    return flush()
  }, [flush])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      window.clearTimeout(timerRef.current)
      window.clearTimeout(idleTimerRef.current)
      void flush()
    }
  }, [flush])

  return { state, schedule, flush, retry }
}
