import { useSyncExternalStore } from 'react'
import type { AgentSession } from '@/types/flow'

/**
 * Replies that are still streaming, kept outside the Agent page.
 *
 * Leaving the page doesn't stop a reply (Linear keeps working too), but the
 * page that started the stream unmounts. Keeping the streamed session here
 * lets the chat show the live reply again when it's reopened, instead of an
 * older copy without it.
 */
const live = new Map<string, AgentSession>()
const listeners = new Set<() => void>()
let version = 0

function emit() {
  version += 1
  for (const listener of listeners) listener()
}

export function setLiveAgentSession(session: AgentSession) {
  live.set(session.id, session)
  emit()
}

export function clearLiveAgentSession(id: string | undefined) {
  if (id && live.delete(id)) emit()
}

export function liveAgentSession(idOrSlug: string | undefined): AgentSession | undefined {
  if (!idOrSlug) return undefined
  const direct = live.get(idOrSlug)
  if (direct) return direct
  for (const session of live.values()) if (session.slugId === idOrSlug) return session
  return undefined
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/** Re-renders when any live reply changes; read sessions with `liveAgentSession`. */
export function useLiveAgentSessionsVersion() {
  return useSyncExternalStore(subscribe, () => version, () => version)
}
