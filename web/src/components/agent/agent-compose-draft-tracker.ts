/**
 * LS-0027 AgentComposeDraftTracker — automation compose draft id tracker (ClientStorage).
 * Linear keeps up to 20 live automationAgentComposeDraftIds for 24h.
 */

import { ClientStorage } from '@/lib/client-storage'

const STORAGE_KEY = 'automationAgentComposeDraftIds'
const MAX_ENTRIES = 20
const TTL_MS = 1440 * 60 * 1000

export type AgentComposeDraftEntries = Record<string, number>

export class AgentComposeDraftTracker {
  static add(draftId: string) {
    const next: AgentComposeDraftEntries = {
      ...AgentComposeDraftTracker.readLiveEntries(),
      [draftId]: Date.now(),
    }
    const trimmed = Object.fromEntries(
      Object.entries(next)
        .sort(([, a], [, b]) => b - a)
        .slice(0, MAX_ENTRIES),
    )
    ClientStorage.set(STORAGE_KEY, trimmed)
  }

  static has(draftId: string): boolean {
    return draftId in AgentComposeDraftTracker.readLiveEntries()
  }

  static remove(draftId: string) {
    const entries = AgentComposeDraftTracker.readLiveEntries()
    if (!(draftId in entries)) return
    const { [draftId]: _removed, ...rest } = entries
    if (Object.keys(rest).length === 0) ClientStorage.remove(STORAGE_KEY)
    else ClientStorage.set(STORAGE_KEY, rest)
  }

  static readLiveEntries(): AgentComposeDraftEntries {
    const raw = ClientStorage.get<AgentComposeDraftEntries>(STORAGE_KEY) ?? {}
    const cutoff = Date.now() - TTL_MS
    return Object.fromEntries(Object.entries(raw).filter(([, stamp]) => typeof stamp === 'number' && stamp >= cutoff))
  }

  static clear() {
    ClientStorage.remove(STORAGE_KEY)
  }
}
