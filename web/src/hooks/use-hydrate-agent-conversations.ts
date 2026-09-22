import { useEffect, useRef, useState } from 'react'
import { getAgentSession, listAgentSessions } from '@/lib/api'
import type { AgentSession } from '@/types/flow'
import {
  asPersistedConversation,
  useDeferredHydratedConversation,
  type DeferredHydratableConversation,
} from './use-deferred-hydrated-conversation'

/**
 * LS-0735 useHydrateAgentConversations — multi-conversation hydrate + hydrationRevision.
 * Coordinates with store-side deferred hydrate naming (asPersistedConversation / useDeferred*).
 */

export type HydratableAgentConversation = DeferredHydratableConversation<AgentSession> & {
  hydrationRevision?: number
}

export type UseHydrateAgentConversationsOptions = {
  /** Force re-hydrate when revision bumps (edit message, SSE patch, etc.). */
  hydrationRevision?: number
  enabled?: boolean
}

export function useHydrateAgentConversations(
  conversationIds: string[],
  options: UseHydrateAgentConversationsOptions = {},
): {
  conversations: AgentSession[]
  loading: boolean
  error?: string
  hydrationRevision: number
  reload: () => void
} {
  const { hydrationRevision: externalRevision = 0, enabled = true } = options
  const [conversations, setConversations] = useState<AgentSession[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>()
  const [tick, setTick] = useState(0)
  const revision = externalRevision + tick
  const idsKey = conversationIds.join(',')

  useEffect(() => {
    if (!enabled) return
    const ids = idsKey ? idsKey.split(',').filter(Boolean) : []
    if (!ids.length) {
      setConversations([])
      setError(undefined)
      return
    }
    const abort = new AbortController()
    setLoading(true)
    void (async () => {
      try {
        const next = await Promise.all(ids.map(id => getAgentSession(id)))
        if (!abort.signal.aborted) {
          setConversations(next)
          setError(undefined)
        }
      } catch (reason) {
        if (!abort.signal.aborted) {
          setError(reason instanceof Error ? reason.message : 'Failed to hydrate agent conversations')
        }
      } finally {
        if (!abort.signal.aborted) setLoading(false)
      }
    })()
    return () => abort.abort()
  }, [enabled, idsKey, revision])

  return {
    conversations,
    loading,
    error,
    hydrationRevision: revision,
    reload: () => setTick(value => value + 1),
  }
}

/** Hydrate a single conversation summary after paint (agent-side wiring for LS-0735/0727). */
export function useHydrateAgentConversation(
  summary: AgentSession | null | undefined,
  options?: { persisted?: boolean; hydrationRevision?: number },
): AgentSession | null | undefined {
  const hydrate = useRef((id: string) => getAgentSession(id)).current
  const wrapped = asPersistedConversation(summary, hydrate, { persisted: options?.persisted })
  const hydrated = useDeferredHydratedConversation(wrapped)
  // revision bump re-triggers deferred hydrate via identity change below
  const revision = options?.hydrationRevision ?? 0
  const [revKeyed, setRevKeyed] = useState(hydrated)
  useEffect(() => {
    setRevKeyed(hydrated)
  }, [hydrated, revision])
  return revKeyed
}

/** Prefetch toolbar/page session list once for sidebar open (pairs with LS-0752/0756). */
export function usePrefetchAgentSessions(enabled = true) {
  const [sessions, setSessions] = useState<AgentSession[]>([])
  const [loading, setLoading] = useState(false)
  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    setLoading(true)
    void listAgentSessions()
      .then(next => {
        if (!cancelled) setSessions(next)
      })
      .catch(() => {
        if (!cancelled) setSessions([])
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [enabled])
  return { sessions, loading }
}
