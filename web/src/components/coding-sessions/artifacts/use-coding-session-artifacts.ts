import { useEffect, useRef, useState } from 'react'
import { ApiError } from '@/lib/api-client'
import { fetchCodingSessionArtifacts } from '@/lib/coding-sessions-api'
import type { CodingArtifact } from '@/types/coding-sessions'

export type CodingArtifactsStatus = 'idle' | 'loading' | 'error'

/**
 * A session's captures: `initial` (e.g. `session.artifacts`) when given, else fetched, and refetched when `refreshKey`
 * changes. `expired` refetches once when a signed URL stopped loading (stable contentUrls never expire).
 */
export function useCodingSessionArtifacts(sessionId: string, initial?: CodingArtifact[], refreshKey?: string | number) {
  const [fetched, setFetched] = useState<{ sessionId: string; items: CodingArtifact[] }>()
  const [status, setStatus] = useState<CodingArtifactsStatus>(initial ? 'idle' : 'loading')
  const [reload, setReload] = useState(0)
  const first = useRef({ sessionId, refreshKey })
  const expiredRefetch = useRef(false)
  const hasInitialData = Boolean(initial)

  useEffect(() => {
    const unchanged = first.current.sessionId === sessionId && first.current.refreshKey === refreshKey
    if (hasInitialData && unchanged && reload === 0) return
    let cancelled = false
    setStatus('loading')
    fetchCodingSessionArtifacts(sessionId).then(
      items => { if (!cancelled) { setFetched({ sessionId, items: items ?? [] }); setStatus('idle') } },
      cause => {
        if (cancelled) return
        // Not found: nothing to show here (the session page explains why it can't be read). Only a real failure
        // (network, server) is an error with Retry.
        if (cause instanceof ApiError && cause.status === 404) { setFetched({ sessionId, items: [] }); setStatus('idle') }
        else setStatus('error')
      },
    )
    return () => { cancelled = true }
  }, [sessionId, refreshKey, reload, hasInitialData])

  const items = fetched && fetched.sessionId === sessionId ? fetched.items : initial
  const expired = (artifact: CodingArtifact) => {
    if (artifact.contentUrl || expiredRefetch.current) return
    expiredRefetch.current = true
    setReload(value => value + 1)
  }
  return { items, status, expired, retry: () => setReload(value => value + 1) }
}
