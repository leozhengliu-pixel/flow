import { useEffect, useState } from 'react'

/**
 * Conversation summary that may defer full message hydrate until after paint (LS-0727).
 * Linear: if `persisted`, call `hydrate()` on mount and abort on unmount.
 */
export type DeferredHydratableConversation<T extends { id: string }> = T & {
  persisted?: boolean
  hydrate?: () => Promise<T>
}

export function useDeferredHydratedConversation<T extends { id: string }>(
  conversation: DeferredHydratableConversation<T> | null | undefined,
): DeferredHydratableConversation<T> | null | undefined {
  const [hydrated, setHydrated] = useState(conversation)

  useEffect(() => {
    if (!conversation?.persisted || !conversation.hydrate) {
      setHydrated(conversation)
      return
    }
    let cancelled = false
    conversation
      .hydrate()
      .then((next) => {
        if (!cancelled) setHydrated(next)
      })
      .catch(() => {
        if (!cancelled) setHydrated(conversation)
      })
    return () => {
      cancelled = true
    }
  }, [conversation])

  if (hydrated?.id === conversation?.id) return hydrated
  return conversation
}

/** Wrap an AgentSession-like summary so opening the thread defers REST hydrate. */
export function asPersistedConversation<T extends { id: string }>(
  summary: T | null | undefined,
  hydrate: (id: string) => Promise<T>,
  options?: { persisted?: boolean },
): DeferredHydratableConversation<T> | null | undefined {
  if (!summary) return summary
  const persisted = options?.persisted ?? true
  if (!persisted) return summary
  return {
    ...summary,
    persisted: true,
    hydrate: () => hydrate(summary.id),
  }
}
